package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/ieee0824/bench/api"
	"github.com/okzk/ticker"
	"io/ioutil"
	"log"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	WARKER_NUM = flag.Int("w", runtime.NumCPU(), "worker num")
	PORT       = flag.Int("p", 8080, "port")
)

var resultBuffer = map[string]*api.BenchResult{}

const (
	maxConnsInitial = 5
	maxConnsLimit   = 2048
)

type benchPacket struct {
	token string
	url   string
}

type ElasticSemaphore struct {
	sem chan struct{}
	max int32
}

func NewElasticSemaphore(initial, limit int) *ElasticSemaphore {
	if initial > limit {
		panic("initial should be <= limit")
	}

	// buffered channel の空きではなく、中身をセマフォのカウントにする
	s := make(chan struct{}, limit)
	for i := 0; i < initial; i++ {
		s <- struct{}{}
	}
	return &ElasticSemaphore{
		sem: s,
		max: int32(initial),
	}
}

func (t *ElasticSemaphore) Acquire() {
	// 1秒まってもセマフォを獲得できない場合はそのまま実行する
	select {
	case <-t.sem:
	case <-time.After(time.Second):
		atomic.AddInt32(&t.max, +1)
	}
}

var clients = []*http.Client{}

var sems = []*ElasticSemaphore{}

func checkStatusCode(n int) bool {
	d := n / 100
	return d == 2 || d == 3
}

func warker(wg *sync.WaitGroup, q chan benchPacket, i int) {
	defer wg.Done()
	client := clients[i]
	sem := sems[i]
	for {
		sem.Acquire()
		req, ok := <-q
		if !ok {
			return
		}
		start := time.Now()
		resp, err := client.Get(req.url)
		end := time.Now().Sub(start)
		if _, ok := resultBuffer[req.token]; !ok {
			resultBuffer[req.token] = &api.BenchResult{}
		}
		if resultBuffer[req.token].MaxRespTime < end {
			resultBuffer[req.token].MaxRespTime = end
		}
		if resultBuffer[req.token].MinRespTime > end || resultBuffer[req.token].MinRespTime == 0 {
			resultBuffer[req.token].MinRespTime = end
		}

		if err == nil {
			if checkStatusCode(resp.StatusCode) {
				defer resp.Body.Close()
				sem.Release()
				resultBuffer[req.token].SuccessCount++
			} else {
				sem.Release()
				resultBuffer[req.token].FailCount++
			}

		} else {
			sem.Release()
			resultBuffer[req.token].FailCount++
		}
	}
}

func (t *ElasticSemaphore) Release() {
	select {
	case t.sem <- struct{}{}:
	default:
		atomic.AddInt32(&t.max, -1)
	}
}

func (t *ElasticSemaphore) MaxConns() int32 {
	return atomic.LoadInt32(&t.max)
}

func BenchMarker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	req, err := parseRequest(r)
	if err != nil {
		bin, _ := json.Marshal(map[string]string{"err": err.Error()})
		w.Write(bin)
		return
	}
	if req.WorkerNum > *WARKER_NUM {
		req.WorkerNum = *WARKER_NUM
	} else if req.WorkerNum == 0 {
		req.WorkerNum = *WARKER_NUM
	}

	defer delete(resultBuffer, req.Token)

	var wg sync.WaitGroup
	q := make(chan benchPacket, *WARKER_NUM)

	for i := 0; i < req.WorkerNum; i++ {
		wg.Add(1)
		go warker(&wg, q, i)
	}

	start := time.Now()
	ticker := ticker.New(1*time.Microsecond, func(t time.Time) {
		q <- benchPacket{req.Token, req.URL()}
	})
	time.Sleep(time.Duration(int64(req.Time)) * time.Second)
	ticker.Stop()
	close(q)
	wg.Wait()
	end := time.Now()
	fmt.Printf("%s: %f秒\n", req.Token, (end.Sub(start)).Seconds())

	successCounter := resultBuffer[req.Token].SuccessCount
	failerCounter := resultBuffer[req.Token].FailCount

	totalRespTime := (end.Sub(start))
	avgRespTime := totalRespTime / time.Duration(successCounter+failerCounter)

	bin, err := json.Marshal(struct {
		SuccessCount       int     `json:"success_counter"`
		RequestCountPerSec float64 `json:"request_count_per_sec"`
		FailCount          int     `json:"fail_counter"`
		RespTimeAVG        string  `json:"resp_time_avg"`
		TotalRespTime      string  `json:"total_resp_time"`
		MaxRespTime        string  `json:"max_resp_time"`
		MinRespTime        string  `json:"min_resp_time"`
	}{
		successCounter,
		float64(successCounter) / float64((end.Sub(start)).Seconds()),
		failerCounter,
		avgRespTime.String(),
		totalRespTime.String(),
		resultBuffer[req.Token].MaxRespTime.String(),
		resultBuffer[req.Token].MinRespTime.String(),
	})
	if err != nil {
		bin, _ := json.Marshal(map[string]string{"err": err.Error()})
		w.Write(bin)
		return
	}
	w.Write(bin)
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	result, err := parseRequest(r)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(*result)
}

func parseRequest(r *http.Request) (*api.Request, error) {
	var ret api.Request
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	err = json.Unmarshal(body, &ret)
	if err != nil {
		return nil, err
	}
	if ret.Time == 0 {
		ret.Time = 10
	}
	return &ret, nil
}

func main() {
	flag.Parse()
	clients = func() []*http.Client {
		result := []*http.Client{}
		for i := 0; i < *WARKER_NUM; i++ {
			result = append(result, &http.Client{Timeout: time.Duration(10) * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: maxConnsLimit}})
		}
		return result
	}()
	sems = func() []*ElasticSemaphore {
		result := []*ElasticSemaphore{}
		for i := 0; i < *WARKER_NUM; i++ {
			result = append(result, NewElasticSemaphore(maxConnsInitial, maxConnsLimit))
		}
		return result
	}()

	runtime.GOMAXPROCS(runtime.NumCPU())
	http.HandleFunc("/", BenchMarker)
	http.ListenAndServe(fmt.Sprintf(":%d", *PORT), nil)
}
