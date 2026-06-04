// onboarding-server can be run standalone for UI debugging. The launcher
// embeds the same server with real deps in P9.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/agentserver/agentserver-pkg/internal/ui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "bind address; 0 = random port")
	flag.Parse()

	handler := ui.NewServer(ui.NewNoopOrchestrator())
	ln, err := newListener(*addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	fmt.Printf("onboarding-server: http://%s/\n", ln.Addr())
	log.Fatal(http.Serve(ln, handler))
}
