package main

import master "github.com/codeuniversity/al-master"

const (
	address = "localhost:5000"
	port    = 4000
)

func main() {
	s := master.NewServer(address, port)
	s.InitUniverse()
	s.Run()
}
