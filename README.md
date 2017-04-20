# chasqui — a minimalistic tool for experimenting with HTTP-based bulk file transfer

## Overview
`chasqui` is an experimental, minimalistic, command line tool to evaluate the suitability of HTTP-based bulk file transport over high-latency network links. It is intended to generate synthetic load on HTTP-based file server and measure the performance of doing memory-to-memory transfers.

`chasqui` consists of three components: a file server, a client and a driver. The file server responds to HTTP GET requests emitted by the client. The client established several connections to the file server and emits download requests as a result of commands emitted by the driver.

Both the client and the file server use the Go built-in implementation of HTTP/1.1 and HTTP2. Data exchange is always performed over a secure channel, by using TLS.

## How to use

This is the synopsis of the command:

```bash
$ chasqui

USAGE:
    chasqui server [-addr=<network address>] [-ca=<file>] [-cert=<file>]
                   [-key=<file>]

    chasqui client [-addr=<network address>] [-ca=<file>] [-cert=<file>]
                   [-key=<file>]

    chasqui driver [-clients=<network addresses>] [-servers=<network addresses>]
                   [-duration=duration]

    chasqui -help
    chasqui -version

Use 'chasqui -help' to get detailed information about options and examples
of usage.
```

To use `chasqui` you need at least two hosts: one for running the file server and the other for running the client. The client emits download (i.e. HTTP GET) requests to the file server which sends the synthetized contents of a file in the body of the response. No disk I/O is induced by `chasqui` neither by the file server nor by the client.

First, start a a file server in `hostA`:

```bash
$ chasqui server -addr :5678 -ca ca.pem -cert hostA.cert -key hostA.key
```

Next, start a client in `hostB`:

```bash
$ chasqui client -addr :9443 -ca ca.pem
```

And finally, start a driver. The driver will send commands to the client running in `hostB` to emit download requests to `hostA` and report on the observed results. For instance, the command:

```bash
$ chasqui driver -clients hostB:9443 -servers hostA:5678 -http1 -duration 60s -concurrency 2
```

will instruct the client running in `hostB` to emit HTTP GET requests to the file server running in `hostA` during 60 seconds (`-duration` option). The `-concurrency` option specifies how many simultaneous TCP connections the client will establish with the server.

At the end of each test, the driver will print a report, similar to the one below:

```bash
chasqui: download report
        client:           'hostB.example.org:9443'
        concurrency:      2
        elapsed time:     1m8.263740015s
        files downloaded: 15
        data volume:      1471.47 MB
        download rate:    21.56 MB/sec
        errors:           0
Summary:
   download operations: 15
   data volume:         1471.47 MB
   avg file size:       98.10 MB
   download rate:       21.56 MB/sec
```

You can start several clients and several file servers, each running in a different host. This allows for simultaneous generation of download requests by several clients on several servers.

For more details on the usage of `chasqui driver` do:

```bash
$ chasqui driver --help
```

**WARNING**: this software is highly experimental. Don't use for production purposes. Don't use in hosts exposed to the Internet.

## Installation
To **build from sources**, you need to have installed the [Go programming environment](https://golang.org) and then do:

```
go get -u github.com/airnandez/chasqui
```

## Feedback

Your feedback is welcome. Please feel free to provide it by [opening an issue](https://github.com/airnandez/chasqui/issues).

## Credits

This tool is being developed and maintained by Fabio Hernandez at [IN2P3 / CNRS computing center](http://cc.in2p3.fr) (Lyon, France).

## License
Copyright 2017 Fabio Hernandez

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

[http://www.apache.org/licenses/LICENSE-2.0](http://www.apache.org/licenses/LICENSE-2.0)

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
