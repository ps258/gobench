Introduction
================

This is a fork of https://github.com/cmpxchg16/gobench which doesn't seem to be getting any love at the momement (Sept 2020)

Differences
================
  * Made it simpler to turn off keep alives. -k == keep alives, no -k == no keep alives
  * Added -s to allow acceptance of self signed certificates
  * Added -x and -y so that a certificate and key can be used to test APIs protected by MATLS
  * Converted it to standard net/http which gives similar rates to other benchmarking tools
  * Added a way to attach certificates to do mTLS
  * Added -resolve which allows you to connect to a server which has a certificate DN which doesn't match the URL used to connect

Usage
================

```
Usage of ./gobench:
  -auth string
        Authorization header. Incompatible with -f
  -c int
        Number of concurrent clients (default 100)
  -d string
        HTTP POST data file path
  -dump
        Dump a bunch of replies
  -f string
        URL's file path (line seperated)
  -host string
        Host header to use (independent of URL). Incompatible with -f
  -k    Do HTTP keep-alive
  -m    Track and report the maximum latency as it occurs
  -r int
        Number of requests per client (default -1)
  -resolve string
        Resolve. Like -resolve in curl. Used for the CN/SAN match in a cert. Incompatible with -f
  -s    Skip cert check
  -t int
        Period of time (in seconds) (default -1)
  -tr int
        Read timeout (in milliseconds) (default 5000)
  -tw int
        Write timeout (in milliseconds) (default 5000)
  -u string
        URL. Incompatible with -f
  -x string
        Certificate for MATLS
  -y string
        Key to certificate for MATLS
```


Notes
================

1. I've probably broken stuff, particularly features that I don't use


Help
================

```gobench --help```

License
================

Licensed under the New BSD License.

Author
================

Uri Shamay (shamayuri@gmail.com)
