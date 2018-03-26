// +build darwin dragonfly freebsd linux netbsd openbsd solaris

package main

import "flag"

var (
	unix = flag.String("unix", "",
		`Use instead of -p to indicate listening on a Unix domain socket instead of a
    	TCP port. If present, must be the path to a domain socket.`)
)

func init() {
	getUnixSocket = func() string {
		return *unix
	}
}
