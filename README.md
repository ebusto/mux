The mux package is a simple io.ReadWriteCloser multiplexer, intended for use over reliable but otherwise primitive connections, such as the XBee radio.

# Example
```go
package main

import (
	"net"

	"github.com/ebusto/mux"
)

func main() {
	a, b := net.Pipe()

	ma, mb := mux.New(a), mux.New(b)

	sa0, sb0 := ma.Stream(0), mb.Stream(0)
	sa1, sb1 := ma.Stream(1), mb.Stream(1)

	// The following streams are now connected, each stream implements io.ReadWriter.
	// *  sa0 <--> sb0
	// *  sa1 <--> sb1

	...
}
```

# Frame
The protocol is trivial, with each frame encoding the stream ID as a byte, the length as a variable integer, followed by the payload.
