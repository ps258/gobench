Introduction
================

This is a fork of https://github.com/cmpxchg16/gobench which doesn't seem to be getting any love at the momement (Sept 2020)

Differences
================
  * Fixed a bug in -k that left it on by default. -k now enables keep alives and not specifying -k turns them off
  * Added -s to allow acceptance of self signed certificates
  * Added -x and -y so that a certificate and key can be used to test APIs protected by MATLS

Usage
================

```
Usage of gobench
  -auth string
        Authorization header
  -c int
        Number of concurrent clients (default 100)
  -d string
        HTTP POST data file path
  -f string
        URL's file path (line seperated)
  -k    Do HTTP keep-alive
  -m    Track and report the maximum latency as it occurs
  -r int
        Number of requests per client (default -1)
  -s    Skip cert check
  -t int
        Period of time (in seconds) (default -1)
  -tr int
        Read timeout (in milliseconds) (default 5000)
  -tw int
        Write timeout (in milliseconds) (default 5000)
  -u string
        URL
  -x string
        Certificate for MATLS
  -y string
        Key to certificate for MATLS
```


Notes
================

1. I've probably broken stuff, particularly features that I don't use
2. This uses https://github.com/valyala/fasthttp which gets improbably good results compared to net/http so I worry its cutting too many corners

Help
================

```gobench --help```

License
================

Licensed under the New BSD License.

Author
================

Uri Shamay (shamayuri@gmail.com)
