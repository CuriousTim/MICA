package cablastp

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
)

const (
	FileCoarseFasta      = "coarse.fasta"
	FileCoarseLinks      = "coarse.links"
	FileCoarsePlainLinks = "coarse.links.plain"
	FileCoarseLinksIndex = "coarse.links.index"
	FileCoarseSeeds      = "coarse.seeds"
	FileCoarsePlainSeeds = "coarse.seeds.plain"
)

// CoarseDB represents a set of unique sequences that comprise the "coarse"
// database. Sequences in the ReferenceDB are use to re-create the original
// sequences.
type CoarseDB struct {
	Seqs     []*CoarseSeq
	seqsRead int
	Seeds    Seeds

	FileFasta      *os.File
	FileSeeds      *os.File
	FileLinks      *os.File
	FileLinksIndex *os.File

	seqLock *sync.RWMutex

	readOnly   bool
	plain      bool
	plainLinks *os.File
	plainSeeds *os.File
}

// NewCoarseDB takes a list of initial original sequences, and adds each
// sequence to the reference database unchanged. Seeds are also generated for
// each K-mer in each original sequence.
func NewWriteCoarseDB(appnd bool, db *DB) (*CoarseDB, error) {
	var err error

	Vprintln("\tOpening coarse database...")

	coarsedb := &CoarseDB{
		Seqs:           make([]*CoarseSeq, 0, 10000000),
		seqsRead:       0,
		Seeds:          NewSeeds(db.MapSeedSize, db.LowComplexityWindow),
		FileFasta:      nil,
		FileSeeds:      nil,
		FileLinks:      nil,
		FileLinksIndex: nil,
		seqLock:        &sync.RWMutex{},
		readOnly:       db.ReadOnly,
		plain:          db.SavePlain,
		plainSeeds:     nil,
	}
	coarsedb.FileFasta, err = db.openWriteFile(appnd, FileCoarseFasta)
	if err != nil {
		return nil, err
	}
	coarsedb.FileSeeds, err = db.openWriteFile(appnd, FileCoarseSeeds)
	if err != nil {
		return nil, err
	}
	coarsedb.FileLinks, err = db.openWriteFile(appnd, FileCoarseLinks)
	if err != nil {
		return nil, err
	}
	coarsedb.FileLinksIndex, err = db.openWriteFile(appnd, FileCoarseLinksIndex)
	if err != nil {
		return nil, err
	}

	if coarsedb.plain {
		coarsedb.plainLinks, err = db.openWriteFile(appnd, FileCoarsePlainLinks)
		if err != nil {
			return nil, err
		}
		coarsedb.plainSeeds, err = db.openWriteFile(appnd, FileCoarsePlainSeeds)
		if err != nil {
			return nil, err
		}
	}

	if appnd {
		if err = coarsedb.load(); err != nil {
			return nil, err
		}
	}

	Vprintln("\tDone opening coarse database.")
	return coarsedb, nil
}

func NewReadCoarseDB(db *DB) (*CoarseDB, error) {
	var err error

	Vprintln("\tOpening coarse database...")

	coarsedb := &CoarseDB{
		Seqs:           make([]*CoarseSeq, 0, 10000000),
		Seeds:          NewSeeds(db.MapSeedSize, db.LowComplexityWindow),
		FileFasta:      nil,
		FileSeeds:      nil,
		FileLinks:      nil,
		FileLinksIndex: nil,
		seqLock:        nil,
		readOnly:       false,
		plain:          db.SavePlain,
	}
	coarsedb.FileFasta, err = db.openReadFile(FileCoarseFasta)
	if err != nil {
		return nil, err
	}
	coarsedb.FileLinks, err = db.openReadFile(FileCoarseLinks)
	if err != nil {
		return nil, err
	}
	coarsedb.FileLinksIndex, err = db.openReadFile(FileCoarseLinksIndex)
	if err != nil {
		return nil, err
	}

	if err := coarsedb.load(); err != nil {
		return nil, err
	}

	Vprintln("\tDone opening coarse database.")
	return coarsedb, nil
}

// Add takes an original sequence, converts it to a coarse sequence, and
// adds it as a new coarse sequence to the coarse database. Seeds are
// also generated for each K-mer in the sequence. The resulting coarse
// sequence is returned along with its sequence identifier.
func (coarsedb *CoarseDB) Add(oseq []byte) (int, *CoarseSeq) {
	coarsedb.seqLock.Lock()
	id := len(coarsedb.Seqs)
	corSeq := NewCoarseSeq(id, "", oseq)
	coarsedb.Seqs = append(coarsedb.Seqs, corSeq)
	coarsedb.seqLock.Unlock()

	coarsedb.Seeds.Add(id, corSeq)

	return id, corSeq
}

// CoarseSeqGet is a thread-safe way to retrieve a sequence with index `i`
// from the coarse database.
func (coarsedb *CoarseDB) CoarseSeqGet(i int) *CoarseSeq {
	coarsedb.seqLock.RLock()
	seq := coarsedb.Seqs[i]
	coarsedb.seqLock.RUnlock()

	return seq
}

// Expand will follow all links to compressed sequences for the coarse
// sequence at index `i` and return a slice of decompressed sequences.
func (coarsedb *CoarseDB) Expand(
	comdb *CompressedDB, id int) ([]OriginalSeq, error) {

	// Calculate the byte offset into the coarse links file where the links
	// for the coarse sequence `i` starts.
	off, err := coarsedb.linkOffset(id)
	if err != nil {
		return nil, err
	}

	// Actually seek to that offset.
	newOff, err := coarsedb.FileLinks.Seek(off, os.SEEK_SET)
	if err != nil {
		return nil, err
	} else if newOff != off {
		return nil,
			fmt.Errorf("Tried to seek to offset %d in the coarse links, "+
				"but seeked to %d instead.", off, newOff)
	}

	// Read in the number of links for this sequence.
	// Each link corresponds to a single original sequence.
	var numLinks int32
	err = binary.Read(coarsedb.FileLinks, binary.BigEndian, &numLinks)
	if err != nil {
		return nil, err
	}

	// We use a map as a set of original sequence ids for eliminating 
	// duplicates (since a coarse sequence can point to different pieces of the 
	// same compressed sequence).
	ids := make(map[int32]bool, numLinks)
	oseqs := make([]OriginalSeq, 0, numLinks)
	for i := int32(0); i < numLinks; i++ {
		compLink, err := coarsedb.readLink()
		if err != nil {
			return nil, err
		}
		if ids[compLink.OrgSeqId] {
			continue
		}

		oseq, err := comdb.ReadSeq(coarsedb, int(compLink.OrgSeqId))
		if err != nil {
			return nil, err
		}
		ids[compLink.OrgSeqId] = true
		oseqs = append(oseqs, oseq)
	}

	return oseqs, nil
}

func (coarsedb *CoarseDB) linkOffset(id int) (seqOff int64, err error) {
	tryOff := int64(id) * 8
	realOff, err := coarsedb.FileLinksIndex.Seek(tryOff, os.SEEK_SET)
	if err != nil {
		return
	} else if tryOff != realOff {
		return 0,
			fmt.Errorf("Tried to seek to offset %d in the coarse links index, "+
				"but seeked to %d instead.", tryOff, realOff)
	}
	err = binary.Read(coarsedb.FileLinksIndex, binary.BigEndian, &seqOff)
	return
}

// ReadClose closes all files necessary for reading the coarse database.
func (coarsedb *CoarseDB) ReadClose() {
	coarsedb.FileFasta.Close()
	coarsedb.FileLinks.Close()
	coarsedb.FileLinksIndex.Close()
}

// ReadClose closes all files necessary for writing the coarse database.
func (coarsedb *CoarseDB) WriteClose() {
	coarsedb.FileFasta.Close()
	coarsedb.FileSeeds.Close()
	coarsedb.FileLinks.Close()
	coarsedb.FileLinksIndex.Close()
	if coarsedb.plain {
		coarsedb.plainLinks.Close()
		coarsedb.plainSeeds.Close()
	}
}

func (coarsedb *CoarseDB) load() (err error) {
	if err = coarsedb.readFasta(); err != nil {
		return
	}
	if err = coarsedb.readSeeds(); err != nil {
		return
	}
	if err = coarsedb.readLinks(); err != nil {
		return
	}

	// After we've loaded the coarse database, the file offset should be
	// at the end of each file. For the coarse fasta file, this is
	// exactly what we want. But for the links and seeds files, we need
	// to clear the file and start over (since they are not amenable to
	// appending like the coarse fasta file is).
	// Do the same for plain files.
	trunc := func(f *os.File) (err error) {
		if err = f.Truncate(0); err != nil {
			return
		}
		if _, err = f.Seek(0, os.SEEK_SET); err != nil {
			return
		}
		return nil
	}
	if err = trunc(coarsedb.FileSeeds); err != nil {
		return
	}
	if err = trunc(coarsedb.FileLinks); err != nil {
		return
	}
	if err = trunc(coarsedb.FileLinksIndex); err != nil {
		return
	}
	if coarsedb.plain {
		if err = trunc(coarsedb.plainSeeds); err != nil {
			return
		}
		if err = trunc(coarsedb.plainLinks); err != nil {
			return
		}
	}

	return nil
}

// Save will save the reference database as a coarse FASTA file and a binary
// encoding of all reference links.
func (coarsedb *CoarseDB) Save() error {
	coarsedb.seqLock.RLock()
	defer coarsedb.seqLock.RUnlock()

	errc := make(chan error, 20)
	wg := &sync.WaitGroup{}

	wg.Add(1)
	go func() {
		if err := coarsedb.saveFasta(); err != nil {
			errc <- err
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		if err := coarsedb.saveLinks(); err != nil {
			errc <- err
		}
		wg.Done()
	}()

	if !coarsedb.readOnly {
		wg.Add(1)
		go func() {
			if err := coarsedb.saveSeeds(); err != nil {
				errc <- err
			}
			wg.Done()
		}()
	}
	if coarsedb.plain {
		wg.Add(1)
		go func() {
			if err := coarsedb.saveLinksPlain(); err != nil {
				errc <- err
			}
			wg.Done()
		}()
		if !coarsedb.readOnly {
			wg.Add(1)
			go func() {
				if err := coarsedb.saveSeedsPlain(); err != nil {
					errc <- err
				}
				wg.Done()
			}()
		}
	}
	wg.Wait()

	// If there's something in the error channel, pop off the first
	// error and return that.
	if len(errc) > 0 {
		return <-errc
	}
	return nil
}
