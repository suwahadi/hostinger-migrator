package pipeline_test

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/tests/sshtest"
	"hostinger-migrator/tests/testenv"
)

type sshtestResult = sshtest.ExecResult

func websiteFor(domain, docroot string) hostinger.Website {
	return hostinger.Website{Domain: domain, Username: testenv.DstAccount, OrderID: 201, VhostType: "addon", IsEnabled: true, RootDirectory: docroot}
}

func makeTarGz(src, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	err = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = "./" + filepath.ToSlash(rel)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			_, err = io.Copy(tw, in)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func gzipFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	gz.Write(in)
	gz.Write([]byte("-- Dump completed on 2026-11-22\n"))
	return gz.Close()
}
