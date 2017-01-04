package mux

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"sync"
)

type Writer interface {
	io.ByteWriter
	io.Writer
}

type Mux struct {
	br *bufio.Reader
	bw *bufio.Writer
	lr *io.LimitedReader // Limited reader from br.
	sm map[byte]*Stream  // Active streams.
	nw chan Writer       // Request for a Writer.

	sync.Mutex
}

type Stream struct {
	id byte
	br *bytes.Buffer // Read buffer.
	bs []byte        // When writing, size buffer. When reading, byte buffer.
	wr bool          // Read wakeup required.
	nr chan bool     // Wait to read.
	nw chan Writer   // Wait to write.

	sync.Mutex
}

func New(cn io.ReadWriter) *Mux {
	br := bufio.NewReader(cn)
	bw := bufio.NewWriter(cn)
	lr := &io.LimitedReader{R: br}

	sm := make(map[byte]*Stream)
	nw := make(chan Writer)

	m := &Mux{br, bw, lr, sm, nw, sync.Mutex{}}

	go m.relayRead()
	go m.relayWrite()

	return m
}

func (m *Mux) relayRead() {
	for {
		// Read the stream ID.
		id, err := m.br.ReadByte()

		if err != nil {
			panic(err)
		}

		// Read the frame size.
		size, err := binary.ReadVarint(m.br)

		if err != nil {
			panic(err)
		}

		// Zero bytes? Don't bother.
		if size == 0 {
			continue
		}

		s := m.Stream(id)

		// Don't read beyond this single frame.
		m.lr.N = size

		s.Lock()

		if _, err := s.br.ReadFrom(m.lr); err != nil {
			panic(err)
		}

		// If the stream is waiting to read, wake it up.
		if s.wr == true {
			s.wr = false
			s.nr <- true
		}

		s.Unlock()
	}
}

func (m *Mux) relayWrite() {
	for {
		m.nw <- m.bw
		<-m.nw
		m.bw.Flush()
	}
}

func (m *Mux) Stream(id byte) *Stream {
	m.Lock()

	if _, ok := m.sm[id]; !ok {
		br := new(bytes.Buffer)
		bs := make([]byte, binary.MaxVarintLen64)
		nr := make(chan bool)

		m.sm[id] = &Stream{id, br, bs, false, nr, m.nw, sync.Mutex{}}
	}

	s := m.sm[id]

	m.Unlock()

	return s
}

func (s *Stream) Read(p []byte) (int, error) {
	s.Lock()

	// Zero bytes ready?
	if s.br.Len() == 0 {
		// Unlock so the mux reader can fill our read buffer.
		s.wr = true
		s.Unlock()

		// Wait for wakeup.
		<-s.nr

		// Reacquire so we can safely read from our read buffer.
		s.Lock()
	}

	n, err := s.br.Read(p)

	s.Unlock()

	return n, err
}

func (s *Stream) Write(p []byte) (int, error) {
	s.Lock()

	// Store the frame size.
	n := binary.PutVarint(s.bs, int64(len(p)))

	// Acquire a Writer.
	w := <-s.nw

	// Write the stream ID.
	if err := w.WriteByte(s.id); err != nil {
		panic(err)
	}

	// Write the frame size.
	if _, err := w.Write(s.bs[:n]); err != nil {
		panic(err)
	}

	// Write the frame data.
	n, err := w.Write(p)

	// Return the Writer.
	s.nw <- w

	s.Unlock()

	return n, err
}
