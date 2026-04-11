module github.com/lapingvino/devotional-pwa

go 1.21.0

require github.com/phpdave11/gofpdf v1.4.2

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/go-sql-driver/mysql v1.9.3 // indirect
)

replace github.com/phpdave11/gofpdf => ./gofpdf-patch
