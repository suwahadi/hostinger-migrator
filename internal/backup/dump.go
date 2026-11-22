package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type DumpStats struct {
	Tables    []string         `json:"tabel"`
	Rows      map[string]int64 `json:"baris"`
	Completed bool             `json:"dump_lengkap"`
}

func (d DumpStats) TotalRows() int64 {
	var n int64
	for _, v := range d.Rows {
		n += v
	}
	return n
}

func tableName(rest string) (string, string) {
	if !strings.HasPrefix(rest, "`") {
		return "", rest
	}
	end := strings.Index(rest[1:], "`")
	if end < 0 {
		return "", rest
	}
	return rest[1 : end+1], rest[end+2:]
}

func CountTuples(values string) int64 {
	var n int64
	depth := 0
	inStr := false
	escaped := false
	for i := 0; i < len(values); i++ {
		c := values[i]
		if inStr {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '\'':
				inStr = false
			}
			continue
		}
		switch c {
		case '\'':
			inStr = true
		case '(':
			if depth == 0 {
				n++
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				return n
			}
		}
	}
	return n
}

func ParseDump(r io.Reader) (DumpStats, error) {
	st := DumpStats{Rows: map[string]int64{}}
	br := bufio.NewReaderSize(r, 1<<20)
	seen := map[string]bool{}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			switch {
			case strings.HasPrefix(line, "CREATE TABLE "):
				name, _ := tableName(strings.TrimPrefix(strings.TrimPrefix(line, "CREATE TABLE "), "IF NOT EXISTS "))
				if name != "" && !seen[name] {
					seen[name] = true
					st.Tables = append(st.Tables, name)
					st.Rows[name] += 0
				}
			case strings.HasPrefix(line, "INSERT INTO "):
				name, rest := tableName(strings.TrimPrefix(line, "INSERT INTO "))
				if idx := strings.Index(rest, "VALUES"); name != "" && idx >= 0 {
					st.Rows[name] += CountTuples(rest[idx+len("VALUES"):])
				}
			case strings.HasPrefix(line, "-- Dump completed"):
				st.Completed = true
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return st, err
		}
	}
	return st, nil
}

func ParseDumpFile(path string) (DumpStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return DumpStats{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return DumpStats{}, fmt.Errorf("dump %s bukan gzip valid: %w", path, err)
	}
	defer gz.Close()
	st, err := ParseDump(gz)
	if err != nil {
		return st, fmt.Errorf("dump %s rusak: %w", path, err)
	}
	return st, nil
}

func CompareDumps(src, dst DumpStats) []string {
	var diffs []string
	for _, t := range src.Tables {
		if dst.Rows[t] != src.Rows[t] {
			if _, ok := dst.Rows[t]; !ok {
				diffs = append(diffs, fmt.Sprintf("tabel %s tidak ada di tujuan", t))
				continue
			}
			diffs = append(diffs, fmt.Sprintf("tabel %s: sumber %d baris, tujuan %d baris", t, src.Rows[t], dst.Rows[t]))
		}
	}
	if len(dst.Tables) != len(src.Tables) && len(diffs) == 0 {
		diffs = append(diffs, fmt.Sprintf("jumlah tabel sumber %d, tujuan %d", len(src.Tables), len(dst.Tables)))
	}
	return diffs
}

type ArchiveStats struct {
	Entries int64 `json:"entri"`
	Files   int64 `json:"file"`
	Bytes   int64 `json:"byte_isi"`
}

func InspectTarGz(path string) (ArchiveStats, error) {
	var st ArchiveStats
	f, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return st, fmt.Errorf("arsip bukan gzip valid: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return st, fmt.Errorf("arsip tar rusak setelah %d entri: %w", st.Entries, err)
		}
		st.Entries++
		if h.Typeflag == tar.TypeReg {
			st.Files++
			n, err := io.Copy(io.Discard, tr)
			if err != nil {
				return st, fmt.Errorf("isi arsip rusak: %w", err)
			}
			st.Bytes += n
		}
	}
	if st.Entries == 0 {
		return st, errors.New("arsip kosong")
	}
	return st, nil
}

func FileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
