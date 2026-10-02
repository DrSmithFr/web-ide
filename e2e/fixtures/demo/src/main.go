package main

import "fmt"

// Greeter says hello.
type Greeter struct{ Name string }

func (g Greeter) Hello() string {
	return fmt.Sprintf("Bonjour %s", g.Name)
}

func main() {
	fmt.Println(Greeter{Name: "monde"}.Hello())
}
