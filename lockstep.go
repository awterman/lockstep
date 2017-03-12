// Package lockstep is a online module for multi-player standalone games.
// Because lockstep just simulates the operations, it highly relies on the quality of network. There is no tolerance for packet loss or lag.
package lockstep

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"time"
)

type Operation int // consider using interface{} instead.

type Instruction struct {
	Op Operation
	Ts time.Duration
}

type Frame struct {
	Inss []Instruction
}

func (this *Frame) merge(other *Frame) {
	var (
		newInssLen               = len(this.Inss) + len(other.Inss)
		newInss    []Instruction = make([]Instruction, newInssLen)

		i = 0 // this
		j = 0 // other
	)

	// "||" means that we compare every element, for the sake of shorter code.
	for i < len(this.Inss) || j < len(other.Inss) {
		if this.Inss[i].Ts < other.Inss[j].Ts {
			newInss = append(newInss, this.Inss[i])
			i++
		} else {
			newInss = append(newInss, other.Inss[j])
			j++
		}
	}

	this.Inss = newInss
}

type lockStep struct {
	frameSpan time.Duration
	startTime time.Time

	frames          []Frame
	topFrameEndTime time.Duration
}

func newLockStep(frameSpan time.Duration, startTime time.Time) *lockStep {
	return &lockStep{
		frameSpan:       frameSpan,
		startTime:       startTime,
		topFrameEndTime: frameSpan,
	}
}

func (p *lockStep) appendOperation(op Operation) {
	ins := Instruction{
		Op: op,
		Ts: time.Now().Sub(p.startTime),
	}

	if ins.Ts > p.topFrameEndTime {
		p.frames = append(p.frames, Frame{})
	}

	frame := &p.frames[len(p.frames)-1]
	frame.Inss = append(frame.Inss, ins)
}

func (p *lockStep) exchange(r io.Reader, w io.Writer) error {
	// json encoding is slow but easy to use.

	localFrame := p.frames[len(p.frames)-2]
	go func() {
		j, _ := json.Marshal(&localFrame)
		len_ := len(j)
		err := binary.Write(w, binary.LittleEndian, len_)
		if err != nil {
			return
		}
		w.Write(j)
	}()

	var len_ int
	err := binary.Read(r, binary.LittleEndian, &len_)
	if err != nil {
		return err
	}

	j := make([]byte, len_)
	_, err = r.Read(j)
	if err != nil {
		return err
	}

	var remoteFrame Frame
	err = json.Unmarshal(j, &remoteFrame)
	if err != nil {
		return err
	}

	p.frames[len(p.frames)-2].merge(&remoteFrame)
	return nil
}

type Instance struct {
	Ops    chan<- Operation
	Frames <-chan Frame
	Errs   <-chan error

	quits chan<- bool
}

func New(frameSpan time.Duration, r io.Reader, w io.Writer) *Instance {
	ls := newLockStep(frameSpan, time.Now())

	ops := make(chan Operation)
	frames := make(chan Frame)
	errs := make(chan error, 1)
	quits := make(chan bool, 2) // the capacity is the number of go routines.

	instance := &Instance{
		Ops:    ops,
		Frames: frames,
		Errs:   errs,

		quits: quits,
	}

	// get op.
	go func() {
		select {
		case op := <-ops:
			ls.appendOperation(op)
		case <-quits:
			return
		}
	}()

	// emit merged frame.
	ticker := time.NewTicker(frameSpan)
	go func() {
		select {
		case <-ticker.C:
			err := ls.exchange(r, w)
			if err != nil {
				errs <- err
				instance.Stop()
			}

			// consider the second frame to the last finished merging.
			frames <- ls.frames[len(ls.frames)-2]

		case <-quits:
			ticker.Stop()
			close(frames)
			close(errs)
			return
		}
	}()

	return instance
}

func (p *Instance) Stop() {
	for i := 0; i < cap(p.quits); i++ {
		p.quits <- true
	}

	close(p.quits)
}
