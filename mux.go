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

	sm map[byte]*Stream // Active streams.
	nw chan Writer      // Request for a Writer.

	sync.Mutex
}

type Stream struct {
	id byte

	br *bytes.Buffer // Read buffer.
	nr chan bool     // Waiting to read.
	nw chan Writer   // Waiting to write.

	sync.Mutex
}

func New(cn io.ReadWriter) *Mux {
	br := bufio.NewReader(cn)
	bw := bufio.NewWriter(cn)

	sm := make(map[byte]*Stream)
	nw := make(chan Writer)

	m := &Mux{br, bw, sm, nw, sync.Mutex{}}

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

		m.Lock()
		s, ok := m.sm[id]
		m.Unlock()

		// Unknown stream? Discard.
		if !ok {
			m.br.Discard(int(size))
			continue
		}

		s.Lock()

		// Copy to the stream's read buffer.
		if _, err := io.CopyN(s.br, m.br, size); err != nil {
			panic(err)
		}

		// If the stream is blocking on a read, wake it up.
		select {
		case s.nr <- true:
		default:
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
	br := new(bytes.Buffer)
	nr := make(chan bool)

	s := &Stream{id, br, nr, m.nw, sync.Mutex{}}

	m.Lock()
	m.sm[id] = s
	m.Unlock()

	return s
}

func (s *Stream) Read(p []byte) (int, error) {
	s.Lock()

	// Zero bytes ready?
	if s.br.Len() == 0 {
		// Unlock so the reader can fill our read buffer.
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
	// Acquire a Writer.
	w := <-s.nw

	// Write the stream ID.
	if err := w.WriteByte(s.id); err != nil {
		panic(err)
	}

	// Write the frame size.
	b := make([]byte, binary.MaxVarintLen64)
	n := binary.PutVarint(b, int64(len(p)))

	if _, err := w.Write(b[:n]); err != nil {
		panic(err)
	}

	// Write the frame data.
	n, err := w.Write(p)

	// Return the Writer.
	s.nw <- w

	return n, err
}
