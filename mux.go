package mux

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"sync"
)

var streamOrder = binary.LittleEndian

type (
	streamId   uint8
	streamSize uint32
)

type Mux struct {
	br *bufio.Reader
	bw *bufio.Writer

	sm map[streamId]*Stream // Active streams.
	nw chan streamId        // Stream requests a write.

	sync.Mutex
}

type Stream struct {
	id streamId

	br *bytes.Buffer
	bw *bytes.Buffer

	nr chan bool     // Stream notified of a read.
	nw chan streamId // Stream requests a write.
	wr bool          // Stream waiting for a read?

	sync.Mutex
}

func New(cn io.ReadWriter) *Mux {
	br := bufio.NewReader(cn)
	bw := bufio.NewWriter(cn)

	sm := make(map[streamId]*Stream)
	nw := make(chan streamId)

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
		var id streamId
		var sz streamSize

		if err := binary.Read(m.br, streamOrder, &id); err != nil {
			panic(err)
		}

		if err := binary.Read(m.br, streamOrder, &sz); err != nil {
			panic(err)
		}

		if sz == 0 {
			continue
		}

		m.Lock()
		s, ok := m.sm[id]
		m.Unlock()

		// Unknown stream? Discard.
		if !ok {
			m.br.Discard(int(sz))
			continue
		}

		s.Lock()

		if _, err := io.CopyN(s.br, m.br, int64(sz)); err != nil {
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
	for id := range m.nw {
		m.Lock()
		s := m.sm[id]
		m.Unlock()

		// If the stream doesn't exist, this will SIGSEGV. As it should.
		s.Lock()

		if err := binary.Write(m.bw, streamOrder, id); err != nil {
			panic(err)
		}

		// TODO: The write buffer may be larger than a uint32 can represent.
		if err := binary.Write(m.bw, streamOrder, streamSize(s.bw.Len())); err != nil {
			panic(err)
		}

		if _, err := s.bw.WriteTo(m.bw); err != nil {
			panic(err)
		}

		s.Unlock()

		m.bw.Flush()
	}
}

func (m *Mux) Stream(id streamId) *Stream {
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
