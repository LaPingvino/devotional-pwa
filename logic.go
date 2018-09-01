// +build js

package main

import (
	"honnef.co/go/js/dom"
	"fmt"
)

func nav(n map[string]string) {
	a := func(t, h string) string {
		return fmt.Sprintf(`<a class="mdl-navigation__link" href="%s">%s</a>`, h, t)
	}
	el := dom.GetWindow().Document().QuerySelector("nav")
	var menu string
	for t, h := range n {
		menu += a(t, h)
	}
	el.SetInnerHTML(menu)
}

func main() {
	menu := map[string]string {
		"en": "#en",
	}
	nav(menu)
}
