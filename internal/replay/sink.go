// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"compress/gzip"
	"io"
	"os"
	"strings"
)

// Create opens a replay file for writing, gzipping it when the path ends in
// .gz. An empty path returns a nil sink, which NewRecorder turns into a nil
// *Recorder — so "not recording" needs no branch at any call site.
func Create(path string) (io.WriteCloser, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".gz") {
		return f, nil
	}
	return &gzipSink{gz: gzip.NewWriter(f), f: f}, nil
}

// gzipSink closes the compressor before the file, which a plain io.MultiCloser
// would get wrong: closing the file first loses the gzip trailer.
type gzipSink struct {
	gz *gzip.Writer
	f  *os.File
}

func (s *gzipSink) Write(p []byte) (int, error) { return s.gz.Write(p) }

func (s *gzipSink) Flush() error { return s.gz.Flush() }

func (s *gzipSink) Close() error {
	if err := s.gz.Close(); err != nil {
		s.f.Close()
		return err
	}
	return s.f.Close()
}

// MemSink buffers a recording in memory. It exists for tests, and for the
// browser build, where there is no filesystem to write to.
type MemSink struct {
	buf []byte
}

func (m *MemSink) Write(p []byte) (int, error) {
	m.buf = append(m.buf, p...)
	return len(p), nil
}

func (m *MemSink) Close() error { return nil }

// Bytes returns what has been recorded so far.
func (m *MemSink) Bytes() []byte { return m.buf }
