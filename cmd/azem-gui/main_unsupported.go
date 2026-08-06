//go:build !darwin && !windows

package main

import "fmt"

func main() {
	fmt.Println("azem-gui currently supports macOS and Windows")
}
