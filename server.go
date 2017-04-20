package main

import (
	"flag"
	"os"
	"strings"

	"github.com/airnandez/chasqui/fileserver"
)

type serverConfig struct {
	// Command line options
	help bool
	addr string
	ca   string
	cert string
	key  string
}

func serverCmd() command {
	fset := flag.NewFlagSet("chasqui server", flag.ExitOnError)
	config := serverConfig{}

	fset.BoolVar(&config.help, "help", false, "")
	fset.StringVar(&config.addr, "addr", defaultServerAddr, "")
	fset.StringVar(&config.ca, "ca", "ca.pem", "")
	fset.StringVar(&config.cert, "cert", "cert.pem", "")
	fset.StringVar(&config.key, "key", "key.pem", "")
	run := func(args []string) error {
		fset.Usage = func() { serverUsage(args[0], os.Stderr) }
		fset.Parse(args[1:])
		return serverRun(args[0], config)
	}
	return command{fset: fset, run: run}
}

func serverRun(cmdName string, config serverConfig) error {
	if config.help {
		serverUsage(cmdName, os.Stderr)
		return nil
	}
	debug(1, "running server with:")
	debug(1, "   ca='%s'\n", config.ca)
	debug(1, "   cert='%s'\n", config.cert)
	debug(1, "   key='%s'\n", config.key)
	debug(1, "   addr='%s'\n", config.addr)

	fs, err := fileserver.NewServer(config.addr, config.cert, config.key, config.ca)
	if err != nil {
		return err
	}
	return fs.Serve()
}

//  masterUsage prints the usage information about the 'master' subcommand
func serverUsage(cmd string, f *os.File) {
	const serverTempl = `
USAGE:
{{.Tab1}}{{.AppName}} {{.SubCmd}} [-addr=<network address>] [-ca=<file>] [-cert=<file>]
{{.Tab1}}{{.AppNameFiller}} {{.SubCmdFiller}} [-key=<file>]
{{.Tab1}}{{.AppName}} {{.SubCmd}} -help

DESCRIPTION:
{{.Tab1}}'{{.AppName}} {{.SubCmd}}' starts an HTTP2 file server on the local host for
{{.Tab1}}serving file download requests submitted by client processes.
{{.Tab1}}Only one server process is necessary for each host involved in a test
{{.Tab1}}campaign.

OPTIONS:
{{.Tab1}}-addr=<network address>
{{.Tab2}}specifies the network address this server listens to for incoming
{{.Tab2}}requests. The form of each address is 'interface:port', for
{{.Tab2}}instance '127.0.0.1:443'.
{{.Tab2}}Default: {{.DefaultServerAddr}}

{{.Tab1}}-ca=<file>
{{.Tab2}}specifies the path of the PEM-formatted file which contains the
{{.Tab2}}certificates of the certification authorities this server accepts.
{{.Tab2}}To be authenticated, clients of this server are required to identify
{{.Tab2}}themselves by presenting a certificate chain issued by the authorities
{{.Tab2}}included in this file.
{{.Tab2}}Default: ca.pem

{{.Tab1}}-cert=<file>
{{.Tab2}}specifies the path of the PEM-formatted file which contains the
{{.Tab2}}certificate this server process presents to its clients.
{{.Tab2}}Default: cert.pem

{{.Tab1}}-key=<file>
{{.Tab2}}path of the PEM-formatted file which contains the private key of
{{.Tab2}}the certificate specified with the '-cert' option.
{{.Tab2}}Default: key.pem

{{.Tab1}}-help
{{.Tab2}}print this help
`
	tmplFields["SubCmd"] = cmd
	tmplFields["SubCmdFiller"] = strings.Repeat(" ", len(cmd))
	tmplFields["DefaultServerAddr"] = defaultServerAddr
	render(serverTempl, tmplFields, f)
}
