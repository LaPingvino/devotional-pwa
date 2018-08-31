// +build js

package main

import "honnef.co/go/js/dom"

func main() {
	el := dom.GetWindow().Document().QuerySelector("nav")
	el.SetInnerHTML(`<a class="mdl-navigation__link" href="javascript:alert('Yay!');">Javascript is activated!`)
}
