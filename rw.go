package cablastp

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"code.google.com/p/biogo/io/seqio/fasta"
)

func (coarsedb *CoarseDB) readFasta() error {
	Vprintf("Reading %s...\n", FileCoarseFasta)
	timer := time.Now()

	fastaReader := fasta.NewReader(coarsedb.FileFasta)
	for i := 0; true; i++ {
		seq, err := fastaReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		coarsedb.Seqs = append(coarsedb.Seqs, NewBiogoCoarseSeq(i, seq))
	}

	Vprintf("Done reading %s (%s).\n", FileCoarseFasta, time.Since(timer))
	return nil
}

func (coarsedb *CoarseDB) saveFasta() error {
	Vprintf("Writing %s...\n", FileCoarseFasta)
	timer := time.Now()

	bufWriter := bufio.NewWriter(coarsedb.FileFasta)
	for i := coarsedb.seqsRead; i < len(coarsedb.Seqs); i++ {
		_, err := fmt.Fprintf(bufWriter,
			"> %d\n%s\n", i, string(coarsedb.Seqs[i].Residues))
		if err != nil {
			return err
		}
	}
	if err := bufWriter.Flush(); err != nil {
		return err
	}

	Vprintf("Done writing %s (%s).\n", FileCoarseFasta, time.Since(timer))
	return nil
}

func (coarsedb *CoarseDB) readSeeds() error {
	Vprintf("Reading %s...\n", FileCoarseSeeds)
	timer := time.Now()

	gr, err := gzip.NewReader(coarsedb.FileSeeds)
	if err != nil {
		return err
	}

	var hash, cnt, seqInd int32
	var resInd int16
	for {
		if err = binary.Read(gr, binary.BigEndian, &hash); err != nil {
			break
		}
		if err = binary.Read(gr, binary.BigEndian, &cnt); err != nil {
			return err
		}
		for i := int32(0); i < cnt; i++ {
			if err = binary.Read(gr, binary.BigEndian, &seqInd); err != nil {
				return err
			}
			if err = binary.Read(gr, binary.BigEndian, &resInd); err != nil {
				return err
			}

			sl := NewSeedLoc(seqInd, resInd)
			if coarsedb.Seeds.Locs[hash] == nil {
				coarsedb.Seeds.Locs[hash] = sl
			} else {
				lk := coarsedb.Seeds.Locs[hash]
				for ; lk.Next != nil; lk = lk.Next {
				}
				lk.Next = sl
			}
		}
	}
	if err := gr.Close(); err != nil {
		return err
	}

	Vprintf("Done reading %s (%s).\n", FileCoarseSeeds, time.Since(timer))
	return nil
}

func (coarsedb *CoarseDB) saveSeeds() error {
	var i int32

	Vprintf("Writing %s...\n", FileCoarseSeeds)
	timer := time.Now()

	gzipWriter, err := gzip.NewWriterLevel(coarsedb.FileSeeds, gzip.BestSpeed)
	if err != nil {
		return err
	}
	for i = 0; i < int32(coarsedb.Seeds.powers[coarsedb.Seeds.SeedSize]); i++ {
		if coarsedb.Seeds.Locs[i] == nil {
			continue
		}

		if err := binary.Write(gzipWriter, binary.BigEndian, i); err != nil {
			return err
		}

		cnt := int32(0)
		for loc := coarsedb.Seeds.Locs[i]; loc != nil; loc = loc.Next {
			cnt++
		}
		if err := binary.Write(gzipWriter, binary.BigEndian, cnt); err != nil {
			return err
		}
		for loc := coarsedb.Seeds.Locs[i]; loc != nil; loc = loc.Next {
			err = binary.Write(gzipWriter, binary.BigEndian, loc.SeqInd)
			if err != nil {
				return err
			}
			err = binary.Write(gzipWriter, binary.BigEndian, loc.ResInd)
			if err != nil {
				return err
			}
		}
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}

	Vprintf("Done writing %s (%s).\n", FileCoarseSeeds, time.Since(timer))
	return nil
}

func (coarsedb *CoarseDB) saveSeedsPlain() error {
	Vprintf("Writing %s...\n", FileCoarsePlainSeeds)
	timer := time.Now()

	csvWriter := csv.NewWriter(coarsedb.plainSeeds)
	record := make([]string, 0, 10)
	for i := 0; i < coarsedb.Seeds.powers[coarsedb.Seeds.SeedSize]; i++ {
		if coarsedb.Seeds.Locs[i] == nil {
			continue
		}

		record = record[:0]
		record = append(record, string(coarsedb.Seeds.unhashKmer(i)))
		for loc := coarsedb.Seeds.Locs[i]; loc != nil; loc = loc.Next {
			record = append(record,
				fmt.Sprintf("%d", loc.SeqInd),
				fmt.Sprintf("%d", loc.ResInd))
		}
		if err := csvWriter.Write(record); err != nil {
			return err
		}
	}
	csvWriter.Flush()

	Vprintf("Done writing %s (%s).\n", FileCoarsePlainSeeds, time.Since(timer))
	return nil
}

func (coarsedb *CoarseDB) readLinks() error {
	Vprintf("Reading %s...\n", FileCoarseLinks)
	timer := time.Now()

	gr, err := gzip.NewReader(coarsedb.FileLinks)
	if err != nil {
		return err
	}
	br := func(data interface{}) error {
		return binary.Read(gr, binary.BigEndian, data)
	}

	var cnt, orgSeqId int32
	var coarseStart, coarseEnd int16

	for coarseSeqId := 0; true; coarseSeqId++ {
		if err = br(&cnt); err != nil {
			break
		}
		for i := int32(0); i < cnt; i++ {
			if err = br(&orgSeqId); err != nil {
				return err
			}
			if err = br(&coarseStart); err != nil {
				return err
			}
			if err = br(&coarseEnd); err != nil {
				return err
			}
			newLink := NewLinkToCompressed(orgSeqId, coarseStart, coarseEnd)
			coarsedb.Seqs[coarseSeqId].addLink(newLink)
		}
	}
	if err := gr.Close(); err != nil {
		return err
	}

	Vprintf("Done reading %s (%s).\n", FileCoarseLinks, time.Since(timer))
	return nil
}

func (coarsedb *CoarseDB) saveLinks() error {
	Vprintf("Writing %s...\n", FileCoarseLinks)
	timer := time.Now()

	gw, err := gzip.NewWriterLevel(coarsedb.FileLinks, gzip.BestSpeed)
	if err != nil {
		return err
	}
	bw := func(data interface{}) error {
		return binary.Write(gw, binary.BigEndian, data)
	}
	for _, seq := range coarsedb.Seqs {
		cnt := int32(0)
		for link := seq.Links; link != nil; link = link.Next {
			cnt++
		}
		if err = bw(cnt); err != nil {
			return err
		}
		for link := seq.Links; link != nil; link = link.Next {
			if err = bw(link.OrgSeqId); err != nil {
				return err
			}
			if err = bw(link.CoarseStart); err != nil {
				return err
			}
			if err = bw(link.CoarseEnd); err != nil {
				return err
			}
		}
	}
	if err := gw.Close(); err != nil {
		return nil
	}

	Vprintf("Done writing %s (%s).\n", FileCoarseLinks, time.Since(timer))
	return nil
}

func (coarsedb *CoarseDB) saveLinksPlain() error {
	Vprintf("Writing %s...\n", FileCoarsePlainLinks)
	timer := time.Now()

	csvWriter := csv.NewWriter(coarsedb.plainLinks)
	record := make([]string, 0, 10)
	for _, seq := range coarsedb.Seqs {
		record = record[:0]
		for link := seq.Links; link != nil; link = link.Next {
			record = append(record,
				fmt.Sprintf("%d", link.OrgSeqId),
				fmt.Sprintf("%d", link.CoarseStart),
				fmt.Sprintf("%d", link.CoarseEnd))
		}
		if err := csvWriter.Write(record); err != nil {
			return err
		}
	}
	csvWriter.Flush()

	Vprintf("Done writing %s (%s).\n", FileCoarsePlainLinks, time.Since(timer))
	return nil
}

func (comdb *CompressedDB) ReadSeq(
	coarsedb *CoarseDB, orgSeqId int) (OriginalSeq, error) {

	off, err := comdb.orgSeqOffset(orgSeqId)
	if err != nil {
		return OriginalSeq{}, err
	}

	newOff, err := comdb.File.Seek(off, 0)
	if err != nil {
		return OriginalSeq{}, err
	} else if newOff != off {
		return OriginalSeq{},
			fmt.Errorf("Tried to seek to offset %d in the compressed "+
				"database, but seeked to %d instead.", off, newOff)
	}

	record, err := comdb.csvReader.Read()
	if err != nil {
		return OriginalSeq{}, err
	}

	cseq, err := readCompressedSeq(orgSeqId, record)
	if err != nil {
		return OriginalSeq{}, err
	}
	return cseq.Decompress(coarsedb)
}

func (comdb *CompressedDB) orgSeqOffset(id int) (seqOff int64, err error) {
	tryOff := int64(id) * 8
	realOff, err := comdb.Index.Seek(tryOff, 0)
	if err != nil {
		return 0, err
	} else if tryOff != realOff {
		return 0,
			fmt.Errorf("Tried to seek to offset %d in the compressed index, "+
				"but seeked to %d instead.", tryOff, realOff)
	}
	err = binary.Read(comdb.Index, binary.BigEndian, &seqOff)
	return
}

func readCompressedSeq(id int, record []string) (CompressedSeq, error) {
	cseq := CompressedSeq{
		Id:    id,
		Name:  string([]byte(record[0])),
		Links: make([]LinkToCoarse, (len(record)-1)/4),
	}

	for i := 1; i < len(record); i += 4 {
		coarseSeqId64, err := strconv.Atoi(record[i+0])
		if err != nil {
			return CompressedSeq{}, nil
		}
		coarseStart64, err := strconv.Atoi(record[i+1])
		if err != nil {
			return CompressedSeq{}, nil
		}
		coarseEnd64, err := strconv.Atoi(record[i+2])
		if err != nil {
			return CompressedSeq{}, nil
		}
		lk := NewLinkToCoarseNoDiff(
			int(coarseSeqId64), int(coarseStart64), int(coarseEnd64))
		lk.Diff = string([]byte(record[i+3]))

		cseq.Add(lk)
	}
	return cseq, nil
}

func (comdb *CompressedDB) writer() {
	var record []string
	var err error

	byteOffset := int64(0)
	buf := new(bytes.Buffer)
	csvWriter := csv.NewWriter(buf)
	csvWriter.Comma = ','
	csvWriter.UseCRLF = false

	for cseq := range comdb.writerChan {
		// Reset the buffer so it's empty. We want it to only contain
		// the next record we're writing.
		buf.Reset()

		// Allocate memory for creating the next record.
		// A record is a sequence name followed by four-tuples of links:
		// (coarse-seq-id, coarse-start, coarse-end, diff).
		record = make([]string, 0, 1+4*len(cseq.Links))
		record = append(record, cseq.Name)
		for _, link := range cseq.Links {
			record = append(record,
				fmt.Sprintf("%d", link.CoarseSeqId),
				fmt.Sprintf("%d", link.CoarseStart),
				fmt.Sprintf("%d", link.CoarseEnd),
				link.Diff)
		}

		// Write the record to our *buffer* and flush it.
		if err = csvWriter.Write(record); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}
		csvWriter.Flush()

		// Pass the bytes on to the compressed file.
		if _, err = comdb.File.Write(buf.Bytes()); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}

		// Now write the byte offset that points to the start of this record.
		err = binary.Write(comdb.Index, binary.BigEndian, byteOffset)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}

		// Increment the byte offset to be at the end of this record.
		byteOffset += int64(buf.Len())
	}
	comdb.File.Close()
	comdb.writerDone <- struct{}{}
}