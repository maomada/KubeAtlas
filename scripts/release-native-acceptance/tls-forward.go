package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	listen := flag.String("listen", "", "Docker bridge listener")
	upstream := flag.String("upstream", "", "Hub origin on the test bridge")
	cert := flag.String("cert", "", "Test TLS certificate")
	key := flag.String("key", "", "Test TLS key")
	flag.Parse()

	host, _, err := net.SplitHostPort(*listen)
	if err != nil || !net.ParseIP(host).IsPrivate() {
		log.Fatal("the listener must be an explicit private IP")
	}
	origin, err := url.Parse(*upstream)
	if err != nil || origin.Scheme != "http" || !net.ParseIP(origin.Hostname()).IsPrivate() {
		log.Fatal("the test Hub must be on the private Docker bridge")
	}
	server := &http.Server{
		Addr:              *listen,
		Handler:           httputil.NewSingleHostReverseProxy(origin),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(server.ListenAndServeTLS(*cert, *key))
}
