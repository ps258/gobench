package main

import (
  "bufio"
  "bytes"
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
  "sync"
  "sync/atomic"
  "time"
  "crypto/tls"

  "github.com/glentiki/hdrhistogram"
  "github.com/olekukonko/tablewriter"
  "github.com/ttacon/chalk"
  //"github.com/guptarohit/asciigraph"
)

var (
  requests         int64
  period           int64
  clients          int
  targetURL        string
  urlsFilePath     string
  keepAlive        bool
  postDataFilePath string
  writeTimeout     int
  readTimeout      int
  authHeader       string
  insecureSkipVerify bool
  mtlsCertFile     string
  mtlsKeyFile      string
  trackMaxLatency  bool
  hostHeader       string
  dumpResponse     bool
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

func init() {
  flag.Int64Var(&requests, "r", -1, "Number of requests per client")
  flag.IntVar(&clients, "c", 100, "Number of concurrent clients")
  flag.StringVar(&targetURL, "u", "", "URL")
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
  flag.StringVar(&authHeader, "auth", "", "Authorization header")
  flag.StringVar(&hostHeader, "host", "", "Host header to use (indepedant of URL)")
  flag.BoolVar(&dumpResponse, "dump", false, "Dump a bunch of replies")
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

  elapsed := int64(time.Since(startTime).Seconds())

  if elapsed == 0 {
    elapsed = 1
  }

  fmt.Println()
  fmt.Printf("Requests:                       %10d hits\n", requests)
  fmt.Printf("Successful requests:            %10d hits\n", success)
  fmt.Printf("Network failed:                 %10d hits\n", networkFailed)
  fmt.Printf("Bad requests failed (!2xx):     %10d hits\n", badFailed)
  fmt.Printf("Successful requests rate:       %10d hits/sec\n", success/elapsed)
  fmt.Printf("Read throughput:                %10d bytes/sec\n", readThroughput/elapsed)
  fmt.Printf("Write throughput:               %10d bytes/sec\n", writeThroughput/elapsed)
  fmt.Printf("Test time:                      %10d sec\n", elapsed)
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

  d := MyDialer()
  f := func(network string, addr string) (net.Conn, error) {
    return d(targetURL)
  }

  if mtlsCertFile != "" {
    cert, err := tls.LoadX509KeyPair(mtlsCertFile, mtlsKeyFile)
    if err != nil {
      log.Fatal(err)
    }
    configuration.myClient = &http.Client{ Transport: &http.Transport{
        Dial:            f,
        // huge (times 10) performance improvement
        MaxIdleConnsPerHost: clients,
        MaxIdleConns: 100,
        TLSClientConfig: &tls.Config{ InsecureSkipVerify: insecureSkipVerify, Certificates: []tls.Certificate{cert},},
      }, }
  } else {
    configuration.myClient = &http.Client{ Transport: &http.Transport{
        Dial:            f,
        // huge (times 10) performance improvement
        MaxIdleConnsPerHost: clients,
        MaxIdleConns: 100,
        TLSClientConfig: &tls.Config{ InsecureSkipVerify: insecureSkipVerify, }, }, }
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

func client(configuration *Configuration, result *Result, done *sync.WaitGroup, errChan chan error, respChan chan *resp, dumpChan chan string) {

  var size int
  var statusCode int
  //http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 100
  for result.requests < configuration.requests {
    for _, tmpUrl := range configuration.urls {

      req, err := http.NewRequest(configuration.method, tmpUrl, nil)
      if configuration.keepAlive == true {
        req.Header.Set("Connection", "keep-alive")
      } else {
        req.Header.Set("Connection", "close")
      }
      if len(configuration.authHeader) > 0 {
        req.Header.Set("Authorization", configuration.authHeader)
      }
      if &hostHeader != nil {
        req.Host = hostHeader
      }

      latency := time.Now()
      res, err := configuration.myClient.Get(tmpUrl)
      if err != nil {
        errChan <- err
        respChan <- &resp{
          status:  0,
          latency: time.Now().Sub(latency).Milliseconds(),
          size:    0,
        }
        statusCode = 0
      } else {
        defer res.Body.Close()
        body, _ := ioutil.ReadAll(res.Body)
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
          latency: time.Now().Sub(latency).Milliseconds(),
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

  done.Done()
}


func main() {

  startTime := time.Now()
  var done sync.WaitGroup
  var maxLatency int64
  var messageCount int64
  var dumpCount = 5
  maxLatency = -1
  messageCount = 0
  results := make(map[int]*Result)
  latencies := hdrhistogram.New(1, 10000, 5)

  signalChannel := make(chan os.Signal, 2)
  signal.Notify(signalChannel, os.Interrupt)
  flag.Parse()

  respChan := make(chan *resp, 2*clients)
  errChan := make(chan error, 2*clients)
  dumpChan := make(chan string, 2*clients)

  configuration := NewConfiguration()

  goMaxProcs := os.Getenv("GOMAXPROCS")

  if goMaxProcs == "" {
    runtime.GOMAXPROCS(runtime.NumCPU())
  }

  fmt.Printf("Dispatching %d clients\n", clients)

  done.Add(clients)
  for i := 0; i < clients; i++ {
    result := &Result{}
    results[i] = result
    go client(configuration, result, &done, errChan, respChan, dumpChan)

  }
  fmt.Println("Waiting for results...")
  for {
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
            fmt.Println("MaxLatency: message# ", messageCount, " size: ", res.size, "B status:", res.status, " latency:", res.latency, "ms")
          }
        }
      }
    case body := <-dumpChan:
      if dumpCount > 0 {
        fmt.Println("Dump: ", dumpCount, ": ", body)
        dumpCount--
      } else {
        dumpResponse = false
      }
    case _ = <-signalChannel:
      printResults(results, startTime)
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

      os.Exit(0)
    }
  }
}
