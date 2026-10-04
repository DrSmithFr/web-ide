// Command sshtestd runs the SSH server of the tests (password "pw") for the browser tests.
// It prints its port, then serves until killed.
package main

import (
	"fmt"
	"log"
	"net"

	"github.com/DrSmithFr/web-ide/pod/internal/sshtest"
)

func main() {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(l.Addr().(*net.TCPAddr).Port)
	sshtest.Serve(l)
}
