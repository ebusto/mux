package mux

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"sync"
)

type Mux struct {
	br *bufio.Reader
	bw *bufio.Writer

	sm map[byte]*Stream // Active streams.
	nw chan byte        // Stream requests a write.

	sync.Mutex
}

type Stream struct {
	id byte

	br *bytes.Buffer
	bw *bytes.Buffer

	nr chan bool // Stream notified of a read.
	nw chan byte // Stream requests a write.
	wr bool      // Stream waiting for a read?

	sync.Mutex
}

func New(cn io.ReadWriter) *Mux {
	br := bufio.NewReader(cn)
	bw := bufio.NewWriter(cn)

	sm := make(map[byte]*Stream)
	nw := make(chan byte)

	m := &Mux{br, bw, sm, nw, sync.Mutex{}}

	go m.relayRead()
	go m.relayWrite()

	return m
}

func (m *Mux) Close() error {
	close(m.nw)

	return nil
}

func (m *Mux) relayRead() {
	for {
		id, err := m.br.ReadByte()

		if err != nil {
			panic(err)
		}

		size, err := binary.ReadVarint(m.br)

		if err != nil {
			panic(err)
		}

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

		if _, err := io.CopyN(s.br, m.br, size); err != nil {
			panic(err)
		}

		// If the reading stream is blocking on a read, send a wakeup.
		if s.wr {
			s.wr = false
			s.nr <- true
		}

		s.Unlock()
	}
}

func (m *Mux) relayWrite() {
	buf := make([]byte, binary.MaxVarintLen64)

	for id := range m.nw {
		m.Lock()
		s, ok := m.sm[id]
		m.Unlock()

		if !ok {
			panic("undefined stream")
		}

		s.Lock()

		if err := m.bw.WriteByte(id); err != nil {
			panic(err)
		}

		n := binary.PutVarint(buf, int64(s.bw.Len()))

		if _, err := m.bw.Write(buf[:n]); err != nil {
			panic(err)
		}

		if _, err := s.bw.WriteTo(m.bw); err != nil {
			panic(err)
		}

		s.Unlock()

		m.bw.Flush()
	}
}

func (m *Mux) Stream(id byte) *Stream {
	br := new(bytes.Buffer)
	bw := new(bytes.Buffer)
	nr := make(chan bool)

	s := &Stream{id, br, bw, nr, m.nw, false, sync.Mutex{}}

	m.Lock()
	m.sm[id] = s
	m.Unlock()

	return s
}

func (s *Stream) Read(p []byte) (int, error) {
	s.Lock()

	// Zero bytes ready?
	if s.br.Len() == 0 {
		s.wr = true

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
	s.Lock()
	n, err := s.bw.Write(p)
	s.Unlock()

	s.nw <- s.id

	return n, err
}
