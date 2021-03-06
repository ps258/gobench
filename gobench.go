package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/glentiki/hdrhistogram"
	"github.com/olekukonko/tablewriter"
	"github.com/ttacon/chalk"
)

var (
	requests           int64
	period             int64
	clients            int
	targetURL          string
	urlsFilePath       string
	keepAlive          bool
	postDataFilePath   string
	writeTimeout       int
	readTimeout        int
	authHeader         string
	insecureSkipVerify bool
	mtlsCertFile       string
	mtlsKeyFile        string
	trackMaxLatency    bool
	hostHeader         string
	resolve            string
	dumpResponse       bool
	cipherSuite        string
)

type Configuration struct {
	urls       []string
	method     string
	postData   []byte
	requests   int64
	period     int64
	keepAlive  bool
	authHeader string

	myClient *http.Client
}

type Result struct {
	requests      int64
	success       int64
	networkFailed int64
	badFailed     int64
}

type resp struct {
	status  int
	latency int64
	size    int
}

var readThroughput int64
var writeThroughput int64
var cipherSuiteID uint16

type MyConn struct {
	net.Conn
}

func (this *MyConn) Read(b []byte) (n int, err error) {
	len, err := this.Conn.Read(b)

	if err == nil {
		atomic.AddInt64(&readThroughput, int64(len))
	}

	return len, err
}

func (this *MyConn) Write(b []byte) (n int, err error) {
	len, err := this.Conn.Write(b)

	if err == nil {
		atomic.AddInt64(&writeThroughput, int64(len))
	}

	return len, err
}

func checkCipherSuiteName(cipherName string) (bool, uint16) {
	//takes a string and checks for a match in all names
	for _, c := range tls.CipherSuites() {
		if cipherName == c.Name {
			//fmt.Println("[Secure]Found", c.Name)
			return true, c.ID
		}
	}
	for _, c := range tls.InsecureCipherSuites() {
		if cipherName == c.Name {
			//fmt.Println("[Insecure]Found", c.Name)
			return true, c.ID
		}
	}
	return false, uint16(0)
}

func printCipherSuiteNames() {
	//takes a string and checks for a match in all names
	for _, c := range tls.CipherSuites() {
		fmt.Println("Secure", c.Name)
	}
	for _, c := range tls.InsecureCipherSuites() {
		fmt.Println("Insecure", c.Name)
	}
}

func init() {
	flag.Int64Var(&requests, "r", -1, "Number of requests per client")
	flag.IntVar(&clients, "c", 100, "Number of concurrent clients")
	flag.StringVar(&targetURL, "u", "", "URL. Incompatible with -f")
	flag.StringVar(&urlsFilePath, "f", "", "URL's file path (line seperated)")
	flag.BoolVar(&keepAlive, "k", false, "Do HTTP keep-alive")
	flag.BoolVar(&insecureSkipVerify, "s", false, "Skip cert check")
	flag.StringVar(&mtlsCertFile, "x", "", "Certificate for MATLS")
	flag.StringVar(&mtlsKeyFile, "y", "", "Key to certificate for MATLS")
	flag.BoolVar(&trackMaxLatency, "m", false, "Track and report the maximum latency as it occurs")
	flag.StringVar(&postDataFilePath, "d", "", "HTTP POST data file path")
	flag.Int64Var(&period, "t", -1, "Period of time (in seconds)")
	flag.IntVar(&writeTimeout, "tw", 5000, "Write timeout (in milliseconds)")
	flag.IntVar(&readTimeout, "tr", 5000, "Read timeout (in milliseconds)")
	flag.StringVar(&authHeader, "auth", "", "Authorization header. Incompatible with -f")
	flag.StringVar(&hostHeader, "host", "", "Host header to use (independent of URL). Incompatible with -f")
	flag.StringVar(&resolve, "resolve", "", "Resolve. Like -resolve in curl. Used for the CN/SAN match in a cert. Incompatible with -f")
	flag.BoolVar(&dumpResponse, "dump", false, "Dump a bunch of replies")
	flag.StringVar(&cipherSuite, "cipher", "", "TLS Cipher Suite to use in connection")
}

func printResults(results map[int]*Result, startTime time.Time) {
	var requests int64
	var success int64
	var networkFailed int64
	var badFailed int64

	for _, result := range results {
		requests += result.requests
		success += result.success
		networkFailed += result.networkFailed
		badFailed += result.badFailed
	}

	elapsed := float32(time.Since(startTime).Milliseconds())

	if elapsed == 0.0 {
		elapsed = 1.0
	}

	fmt.Println()
	fmt.Printf("Requests:                       %10d hits\n", requests)
	fmt.Printf("Successful requests:            %10d hits\n", success)
	fmt.Printf("Network failed:                 %10d hits\n", networkFailed)
	fmt.Printf("Bad requests failed (!2xx):     %10d hits\n", badFailed)
	fmt.Printf("Successful requests rate:       %10.0f hits/sec\n", float32(success)/(elapsed/1000.0))
	fmt.Printf("Read throughput:                %10.0f bytes/sec\n", float32(readThroughput)/(elapsed/1000.0))
	fmt.Printf("Write throughput:               %10.0f bytes/sec\n", float32(writeThroughput)/(elapsed/1000.0))
	fmt.Printf("Test time:                      %10.2f sec\n", (elapsed / 1000.0))
}

func printLatency(latencies *hdrhistogram.Histogram) {

	fmt.Println("")
	shortLatency := tablewriter.NewWriter(os.Stdout)
	shortLatency.SetRowSeparator("-")
	shortLatency.SetHeader([]string{
		"Stat",
		"2.5%",
		"50%",
		"97.5%",
		"99%",
		"Avg",
		"Stdev",
		"Min",
		"Max",
	})
	shortLatency.SetHeaderColor(tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor})
	shortLatency.Append([]string{
		chalk.Bold.TextStyle("Latency"),
		fmt.Sprintf("%v ms", latencies.ValueAtPercentile(2.5)),
		fmt.Sprintf("%v ms", latencies.ValueAtPercentile(50)),
		fmt.Sprintf("%v ms", latencies.ValueAtPercentile(97.5)),
		fmt.Sprintf("%v ms", latencies.ValueAtPercentile(99)),
		fmt.Sprintf("%.2f ms", latencies.Mean()),
		fmt.Sprintf("%.2f ms", latencies.StdDev()),
		fmt.Sprintf("%v ms", latencies.Min()),
		fmt.Sprintf("%v ms", latencies.Max()),
	})
	shortLatency.Render()
	fmt.Println("")

}

func readLines(path string) (lines []string, err error) {

	var file *os.File
	var part []byte
	var prefix bool

	if file, err = os.Open(path); err != nil {
		return
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	buffer := bytes.NewBuffer(make([]byte, 0))
	for {
		if part, prefix, err = reader.ReadLine(); err != nil {
			break
		}
		buffer.Write(part)
		if !prefix {
			lines = append(lines, buffer.String())
			buffer.Reset()
		}
	}
	if err == io.EOF {
		err = nil
	}
	return
}

func NewConfiguration() *Configuration {

	if urlsFilePath == "" && targetURL == "" {
		flag.Usage()
		os.Exit(1)
	}

	if urlsFilePath != "" && (hostHeader != "" || targetURL != "" || authHeader != "" || resolve != "") {
		flag.Usage()
		os.Exit(1)
	}

	if requests == -1 && period == -1 {
		fmt.Println("Requests or period must be provided")
		flag.Usage()
		os.Exit(1)
	}

	if requests != -1 && period != -1 {
		fmt.Println("Only one should be provided: [requests|period]")
		flag.Usage()
		os.Exit(1)
	}

	if (mtlsKeyFile != "" && mtlsCertFile == "") || (mtlsKeyFile == "" && mtlsCertFile != "") {
		fmt.Println("Both cert and key must be specified if one is")
		flag.Usage()
		os.Exit(1)
	}

	configuration := &Configuration{
		urls:       make([]string, 0),
		method:     "GET",
		postData:   nil,
		keepAlive:  keepAlive,
		requests:   int64((1 << 63) - 1),
		authHeader: authHeader}

	if period != -1 {
		configuration.period = period

		timeout := make(chan bool, 1)
		go func() {
			<-time.After(time.Duration(period) * time.Second)
			timeout <- true
		}()

		go func() {
			<-timeout
			pid := os.Getpid()
			proc, _ := os.FindProcess(pid)
			err := proc.Signal(os.Interrupt)
			if err != nil {
				log.Println(err)
				return
			}
		}()
	}

	if requests != -1 {
		configuration.requests = requests
	}

	if urlsFilePath != "" {
		fileLines, err := readLines(urlsFilePath)

		if err != nil {
			log.Fatalf("Error in ioutil.ReadFile for file: %s Error: %s", urlsFilePath, err)
		}

		configuration.urls = fileLines
	}

	dialer := MyDialer()
	dialFunction := func(network string, addr string) (net.Conn, error) {
		return dialer(targetURL)
	}

	certificateExpectedName := parseHostname(targetURL)
	if resolve != "" {
		certificateExpectedName = resolve
	}

	var cert tls.Certificate
	var err error
	if mtlsCertFile != "" {
		cert, err = tls.LoadX509KeyPair(mtlsCertFile, mtlsKeyFile)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		cert = tls.Certificate{}
	}

	var cipherSuites []uint16
	if cipherSuite != "" {
		cipherSuites = append(cipherSuites, cipherSuiteID)
	}

	configuration.myClient = &http.Client{
		Transport: &http.Transport{
			Dial:                dialFunction,
			MaxIdleConnsPerHost: clients,
			MaxIdleConns:        clients,
			DisableKeepAlives:   !configuration.keepAlive,
			TLSClientConfig: &tls.Config{
				ServerName:         certificateExpectedName,
				InsecureSkipVerify: insecureSkipVerify,
				Certificates:       []tls.Certificate{cert},
				CipherSuites:       cipherSuites,
			},
		},
	}

	if targetURL != "" {
		configuration.urls = append(configuration.urls, targetURL)
	}

	if postDataFilePath != "" {
		configuration.method = "POST"

		data, err := ioutil.ReadFile(postDataFilePath)

		if err != nil {
			log.Fatalf("Error in ioutil.ReadFile for file path: %s Error: %s", postDataFilePath, err)
		}

		configuration.postData = data
	}

	configuration.myClient.Timeout = time.Duration(readTimeout) * time.Millisecond

	return configuration
}

func parseHostname(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		log.Fatal(err)
	}
	return u.Host
}

func parseAddress(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		log.Fatal(err)
	}
	if "" == u.Port() {
		switch scheme := u.Scheme; scheme {
		case "https":
			u.Host = u.Host + ":443"
		case "http":
			u.Host = u.Host + ":80"
		default:
			log.Fatal("Unable to decode scheme ", u.Scheme)
		}
	}
	return u.Host
}

func MyDialer() func(address string) (conn net.Conn, err error) {
	return func(address string) (net.Conn, error) {
		address = parseAddress(address)
		conn, err := net.Dial("tcp", address)
		if err != nil {
			return nil, err
		}

		myConn := &MyConn{Conn: conn}

		return myConn, nil
	}
}

func client(configuration *Configuration, result *Result, errChan chan error, respChan chan *resp, dumpChan chan string, exitChan chan bool) {

	var size int
	var statusCode int
	for result.requests < configuration.requests {
		for _, tmpUrl := range configuration.urls {

			req, err := http.NewRequest(configuration.method, tmpUrl, nil)
			// req.Close is true when keep alives are off. But also set in Transport which seems to do the work
			req.Close = !configuration.keepAlive
			if len(configuration.authHeader) > 0 {
				req.Header.Set("Authorization", configuration.authHeader)
			}
			if &hostHeader != nil {
				req.Host = hostHeader
			}

			requestStartTime := time.Now()
			res, err := configuration.myClient.Do(req)
			requestReplyTime := time.Now()
			elapsed := int64(requestReplyTime.Sub(requestStartTime) / time.Millisecond)

			if err != nil {
				errChan <- err
				respChan <- &resp{
					status:  0,
					latency: elapsed,
					size:    0,
				}
				statusCode = 0
			} else {
				body, _ := ioutil.ReadAll(res.Body)
				res.Body.Close()
				if dumpResponse {
					dumpChan <- string(body)
				}
				size = len(body) + 2
				for key, value := range res.Header {
					for _, s := range value {
						size += len(s) + 2
					}
					size += len(key) + 2
				}
				respChan <- &resp{
					status:  res.StatusCode,
					latency: elapsed,
					size:    size,
				}
				statusCode = res.StatusCode
			}
			result.requests++

			if err != nil {
				result.networkFailed++
				continue
			}

			if statusCode >= 200 && statusCode < 300 {
				result.success++
			} else {
				result.badFailed++
			}
		}
	}

	exitChan <- true
}

func main() {

	startTime := time.Now()
	var dumpCount = 5
	var runningGoroutines int
	var maxLatency = int64(-1)
	var messageCount = int64(0)
	var ok bool
	results := make(map[int]*Result)
	latencies := hdrhistogram.New(1, 10000, 5)

	flag.Parse()
	if cipherSuite != "" {
		if ok, cipherSuiteID = checkCipherSuiteName(cipherSuite); !ok {
			fmt.Println("Error: Unknown cipher suite:", cipherSuite)
			fmt.Println("Valid suites:")
			printCipherSuiteNames()
			os.Exit(1)
		}
	}

	signalChan := make(chan os.Signal, 2)
	signal.Notify(signalChan, os.Interrupt)

	respChan := make(chan *resp, 2*clients)
	errChan := make(chan error, 2*clients)
	dumpChan := make(chan string, 2*clients)
	exitChan := make(chan bool, 2*clients)

	configuration := NewConfiguration()

	goMaxProcs := os.Getenv("GOMAXPROCS")

	if goMaxProcs == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	fmt.Printf("Dispatching %d clients\n", clients)

	runningGoroutines = clients
	for i := 0; i < clients; i++ {
		result := &Result{}
		results[i] = result
		go client(configuration, result, errChan, respChan, dumpChan, exitChan)
	}
	fmt.Println("Waiting for results...")
	for runningGoroutines > 0 {
		select {
		case err := <-errChan:
			fmt.Println("Error: ", err.Error())
		case res := <-respChan:
			if res.status >= 200 && res.status < 300 {
				messageCount++
				latencies.RecordValue(int64(res.latency))
				if trackMaxLatency {
					if maxLatency < 0 || res.latency > maxLatency {
						maxLatency = res.latency
						fmt.Println(messageCount, " latency:", res.latency, "(ms)")
					}
				}
			}
		case body := <-dumpChan:
			if dumpCount > 0 {
				fmt.Println(dumpCount, ": ", body)
				dumpCount--
			} else {
				dumpResponse = false
			}
		case _ = <-exitChan:
			runningGoroutines--
		case _ = <-signalChan:

			runningGoroutines = 0
		}
	}
	printResults(results, startTime)
	printLatency(latencies)
	os.Exit(0)
}
