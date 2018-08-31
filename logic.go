// +build js

package main

import "honnef.co/go/js/dom"

func main() {
	el := dom.GetWindow().Document().QuerySelector("nav")
	el.SetInnerHTML("Javascript is activated!")
}
