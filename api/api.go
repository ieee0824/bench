package api

import (
	"time"
)

type BenchResult struct {
	SuccessCount       int
	RequestCountPerSec float64
	FailCount          int
	MaxRespTime        time.Duration
	MinRespTime        time.Duration
	RespTimeAVG        time.Duration
	TotalRespTime      time.Duration
}

type Request struct {
	Time      int      `json:"time"`
	URLs      []string `json:"urls"`
	Token     string   `json: "token"`
	WorkerNum int      `json:"worker_num"`
	ring      *ring
}

func (r *Request) setURLs() {
	r.ring = ringNew(r.URLs)
}

func (r *Request) URL() string {
	if r.ring == nil {
		r.setURLs()
	}
	return r.ring.get()
}

type ring struct {
	p      int
	buffer []string
}

func ringNew(s []string) *ring {
	return &ring{0, s}
}

func (r *ring) get() string {
	if r.p >= len(r.buffer) {
		r.p = 0
	}
	ret := r.buffer[r.p]
	r.p++
	return ret
}
