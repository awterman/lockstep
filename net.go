package lockstep

import (
	"encoding/binary"
	"io"
	"math/rand"
	"net"
	"sync"
	"time"
)

func roll(r io.Reader, w io.Writer) (first bool, err error) {
	var (
		local  int64
		remote int64
	)

	for {
		rand.Seed(time.Now().UnixNano())
		local = rand.Int63()
		go func() {
			// omit error. if error occures, it will be exposed in following communication.
			binary.Write(w, binary.LittleEndian, local)
		}()
		err := binary.Read(r, binary.LittleEndian, &remote)
		if err != nil {
			return false, err
		}

		if local > remote {
			return true, nil
		}
		if local < remote {
			return false, nil
		}
	}
}

func decideFrameSpan(isFirst bool, r io.Reader, w io.Writer) (time.Duration, error) {
	max := time.Duration(0)
	b := []byte{'0'}

	// w.Write and r.Read have same arguments and return values.
	step1 := w.Write
	step2 := r.Read
	if !isFirst {
		step1, step2 = step2, step1
	}

	for i := 0; i < 100; i++ {
		t := time.Now()

		_, err := step1(b)
		if err != nil {
			return 0, err
		}

		_, err = step2(b)
		if err != nil {
			return 0, err
		}

		duration := time.Now().Sub(t)
		if duration > max {
			max = duration
		}
	}

	frameSpan := time.Duration(float64(max) / 2 * 1.5)
	return frameSpan, nil
}

type timeoutConn struct {
	conn    net.Conn
	timeout time.Duration
}

func (p *timeoutConn) Read(b []byte) (n int, err error) {
	err = p.conn.SetReadDeadline(time.Now().Add(p.timeout))
	if err != nil {
		return 0, err
	}
	return p.conn.Read(b)
}

func (p *timeoutConn) Write(b []byte) (n int, err error) {
	err = p.conn.SetWriteDeadline(time.Now().Add(p.timeout))
	if err != nil {
		return 0, err
	}
	return p.conn.Write(b)
}

// establish simplex connections.
func Connect(remoteHost string) (*Instance, error) {
	var (
		rConn net.Conn
		wConn net.Conn
		wg    sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			var err error
			wConn, err = net.Dial("tcp", remoteHost)
			if err == nil {
				return
			}
		}
	}()

	server, err := net.Listen("tcp", ":5050")
	if err != nil {
		return nil, err
	}

	rConn, err = server.Accept()
	if err != nil {
		return nil, err
	}
	// r connected.

	wg.Wait()
	// w connected.

	r := &timeoutConn{
		conn:    rConn,
		timeout: 100 * time.Millisecond,
	}

	w := &timeoutConn{
		conn:    wConn,
		timeout: 100 * time.Millisecond,
	}

	isFirst, err := roll(r, w)
	if err != nil {
		return nil, err
	}

	frameSpan, err := decideFrameSpan(isFirst, r, w)
	if err != nil {
		return nil, err
	}

	return New(frameSpan, r, w), nil
}
