package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
)

func testHandler(w http.ResponseWriter, r *http.Request) {
	parseRequest(r)
}

func parseRequest(r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatalln(err)
	}
	fmt.Println(string(body))
}

func main() {
	http.HandleFunc("/", testHandler)
	http.ListenAndServe(":8080", nil)
}
