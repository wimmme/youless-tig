// Command youless-sim is a simulated YouLess LS120 for tests and demos: the
// read API with made-up, clock-following data. Not for production.
//
//	youless-sim -listen :8081 [-password secret]
package main

import (
	"flag"
	"log"
	"net/http"
	"time"
	_ "time/tzdata"

	"github.com/wimmme/youless-tig/internal/sim"
)

func main() {
	listen := flag.String("listen", ":80", "listen address")
	password := flag.String("password", "", "require this password (login via /L?w=)")
	noP1 := flag.Bool("no-p1", false, "simulate a device without a P1 smart meter")
	flag.Parse()
	s := &sim.Sim{Loc: time.Local, Password: *password, NoP1: *noP1}
	log.Printf("youless-sim on %s (time zone %s)", *listen, time.Local)
	srv := &http.Server{Addr: *listen, Handler: s, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
