package mux

import (
	"bytes"
	randc "crypto/rand"
	randm "math/rand"
	"net"
	"sync"
	"testing"
	"time"
)

func TestMux(t *testing.T) {
	a, b := net.Pipe()

	ma := New(a)
	mb := New(b)

	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)

		go testMux(t, &wg, streamId(i), ma, mb)
	}

	wg.Wait()
}

func testMux(t *testing.T, wg *sync.WaitGroup, id streamId, ma *Mux, mb *Mux) {
	sa := ma.Stream(id)
	sb := mb.Stream(id)

	src := make([]byte, 128000)

	if _, err := randc.Read(src); err != nil {
		t.Fatal(err)
	}

	ok := make(chan bool)

	go func() {
		i := 0

		for i < len(src) {
			l := i
			h := i + randm.Intn(len(src)-i) + 1

			i = h

			n, err := sa.Write(src[l:h])

			if err != nil {
				t.Fatal(err)
			}

			t.Logf("[%d] wrote %d [%d], total %d", id, n, h-l, i)

			time.Sleep(time.Duration(randm.Intn(6400)+1) * time.Millisecond)
		}

		ok <- true
	}()

	var dst []byte

	go func() {
		buf := make([]byte, 16384)

		for len(dst) != len(src) {
			h := randm.Intn(len(buf))

			n, err := sb.Read(buf[0:h])

			if err != nil {
				t.Fatal(err)
			}

			dst = append(dst, buf[0:n]...)

			t.Logf("[%d] read %d, total %d", id, n, len(dst))

			time.Sleep(time.Duration(randm.Intn(6400)+1) * time.Millisecond)
		}

		ok <- true
	}()

	<-ok
	<-ok

	if !bytes.Equal(src, dst) {
		t.Errorf("[%d] mismatch", id)
	} else {
		t.Logf("[%d] OK", id)
	}

	wg.Done()
}
