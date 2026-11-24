module github.com/dmikusa/memory-calc-web

go 1.27.4

require github.com/paketo-buildpacks/jvm-vendors v0.0.0

require github.com/mattn/go-shellwords v1.0.16 // indirect

// Use the local checkout so the calculator always matches the code you are
// experimenting with. Adjust this path if the jvm-vendors repo lives elsewhere.
replace github.com/paketo-buildpacks/jvm-vendors => ../../paketo-buildpacks/jvm-vendors
