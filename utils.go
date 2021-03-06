package main

import (
	"bytes"
	"compress/gzip"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func pathExists(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err == nil {
		return fi.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}

func fileExists(fileName string) (bool, error) {
	fi, err := os.Stat(fileName)
	if err == nil {
		return !fi.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt16(a, b int16) int16 {
	if a < b {
		return a
	}
	return b
}

func maxInt16(a, b int16) int16 {
	if a > b {
		return a
	}
	return b
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func compress(data []byte) []byte {
	if len(data) > 0 {
		var buff bytes.Buffer
		gzipW := gzip.NewWriter(&buff)

		if gzipW != nil {
			defer gzipW.Close()

			n, err := gzipW.Write(data)
			if err != nil {
				return data
			} else if n > 0 {
				return buff.Bytes()
			}
		}
	}
	return nil
}

func preparePath(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		repSep := '/'
		if os.PathSeparator == '/' {
			repSep = '\\'
		}

		dir = strings.Replace(dir, string(repSep), string(os.PathSeparator), -1)
		dir = filepath.Clean(dir)
	}

	if dir == "" || dir == "." {
		dir = "." + string(os.PathSeparator)
	}

	absDir, err := filepath.Abs(dir)
	if err == nil {
		dir = absDir
	}

	if dir[len(dir)-1] != os.PathSeparator {
		dir += string(os.PathSeparator)
	}
	return dir
}

func decompress(data []byte) []byte {
	if len(data) > 0 {
		cmpReader, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil && cmpReader != nil {
			defer cmpReader.Close()

			data, err = ioutil.ReadAll(cmpReader)
			if err != nil {
				return data
			}
		}
	}
	return nil
}
