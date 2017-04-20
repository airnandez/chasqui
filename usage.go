package main

import (
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"text/template"
)

type usageType uint32

const (
	usageShort usageType = iota
	usageLong
)

func printUsage(f *os.File, kind usageType) {
	const usageTempl = `
USAGE:
{{.Tab1}}{{.AppName}} server [-addr=<network address>] [-ca=<file>] [-cert=<file>]
{{.Tab1}}{{.AppNameFiller}} {{.ServerCmdFiller}} [-key=<file>]

{{.Tab1}}{{.AppName}} client [-addr=<network address>] [-ca=<file>] [-cert=<file>]
{{.Tab1}}{{.AppNameFiller}} {{.ClientCmdFiller}} [-key=<file>]

{{.Tab1}}{{.AppName}} driver [-clients=<network addresses>] [-servers=<network addresses>]
{{.Tab1}}{{.AppNameFiller}} {{.DriverCmdFiller}} [-duration=duration]

{{.Tab1}}{{.AppName}} -help
{{.Tab1}}{{.AppName}} -version
{{if eq .UsageVersion "short"}}
Use '{{.AppName}} -help' to get detailed information about options and examples
of usage.{{else}}

DESCRIPTION:
{{.Tab1}}{{.AppName}} is an set of experimental tools to evaluate the HTTP2 protocol
{{.Tab1}}for bulk file transfer over high-latency networks.

{{.Tab1}}Three kind of components are needed to perform a test campaign: one or
{{.Tab1}}more file servers, one or more clients and exactly one driver. See below
{{.Tab1}}for more information about them.

OPTIONS:
{{.Tab1}}-help
{{.Tab2}}Prints this help

{{.Tab1}}-version
{{.Tab2}}Show detailed version information about this application

SUBCOMMANDS:
{{.Tab1}}server
{{.Tab2}}use this subcommand to start a file server process. A file server
{{.Tab2}}process waits for HTTP GET requests emited by the client process
{{.Tab2}}(see below) and serves them. Only one server process is needed per
{{.Tab2}}server host involved in a test campaign.

{{.Tab2}}Use '{{.AppName}} server -help' for getting detailed help on this
{{.Tab2}}subcommand.

{{.Tab1}}client
{{.Tab2}}use this subcommand to start a client process in a given host. A
{{.Tab2}}client process waits for instructions from the driver process (see
{{.Tab2}}below) and emits download requests against the server processes.
{{.Tab2}}For each request, it downloads the data from the appropiate server
{{.Tab2}}using HTTP2 and reports back to the driver process the result of
{{.Tab2}}each executed download operation.
{{.Tab2}}Only one client process is necessary for each host involved in a test
{{.Tab2}}campaign but you may use several hosts for each test.

{{.Tab2}}Use '{{.AppName}} client -help' for getting detailed help on this
{{.Tab2}}subcommand.

{{.Tab1}}driver
{{.Tab2}}use this subcommand to perform a test campaign. The driver process
{{.Tab2}}sends instructions to its companion client processes to emit download
{{.Tab2}}requests against the server processes. It collects the results of
{{.Tab2}}executing those operations and produces a summary of the test
{{.Tab2}}campaign.

{{.Tab2}}Use '{{.AppName}} driver -help' for getting detailed help on this
{{.Tab2}}subcommand.

{{end}}
`
	tmplFields["ClientCmdFiller"] = strings.Repeat(" ", len("client"))
	tmplFields["ServerCmdFiller"] = strings.Repeat(" ", len("server"))
	tmplFields["DriverCmdFiller"] = strings.Repeat(" ", len("driver"))
	if kind == usageLong {
		tmplFields["UsageVersion"] = "long"
	}
	render(usageTempl, tmplFields, f)
}

func render(tpl string, fields map[string]string, out io.Writer) {
	minWidth, tabWidth, padding := 4, 4, 0
	tabwriter := tabwriter.NewWriter(out, minWidth, tabWidth, padding, byte(' '), 0)
	templ := template.Must(template.New("").Parse(tpl))
	templ.Execute(tabwriter, fields)
	tabwriter.Flush()
}
