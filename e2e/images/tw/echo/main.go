// Command echo-server is the e2e forward target: a TCP listener that echoes
// every byte back, so the suite can verify tunnel traffic byte-for-byte.
package main

import (
	"flag"
	"io"
	"log"
	"net"
)

func main() {
	port := flag.String("port", "7777", "listen port")
	flag.Parse()
	l, err := net.Listen("tcp", "127.0.0.1:"+*port)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("echo listening on 127.0.0.1:%s", *port)
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go func(c net.Conn) {
			defer c.Close()
			io.Copy(c, c)
		}(c)
	}
}
