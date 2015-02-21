package main

import (
	"strings"
	"bufio"
	"os"
	"io/ioutil"
	"html/template"
	"flag"
	"fmt"
	"net"
	"net/http"

	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcrpcclient"
	log "github.com/golang/glog"
)

var port = flag.String("port", "8080", "Port to listen on")
var nodeAddr = flag.String("node_addr", ":8333", "Bitcoin node address")
var bitcoinConf = flag.String("bitcoin_conf", "~/.bitcoin/bitcoin.conf", "Bitcoin configuration file")

func main() {
	flag.Parse()

	connCfg := &btcrpcclient.ConnConfig{
		Host:         "localhost:8332",
		HttpPostMode: true, // Bitcoin core only supports HTTP POST mode
		DisableTLS:   true, // Bitcoin core does not provide TLS by default
	}

	f, err := os.Open(*bitcoinConf)
	if err != nil {
		log.Fatal(err)
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		t := scanner.Text()
		log.Infof("%v", t)
		arr := strings.Split(t, "=")
		if len(arr) != 2 {
			continue
		}
		switch arr[0] {
		case "rpcuser":
			connCfg.User = arr[1]
		case "rpcpassword":
			connCfg.Pass = arr[1]
		}
	}
	log.Infof("Bitcoin conf: %v", connCfg)

	client, err := btcrpcclient.New(connCfg, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Shutdown()

	// Get the current block count.
	blockCount, err := client.GetBlockCount()
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("block count: %v", blockCount)

	go handleBitcoinStream()

	log.Infof("Listening at %v...", *port)
	http.HandleFunc("/", nodezHandler)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

func nodezHandler(w http.ResponseWriter, r *http.Request) {
	// TODO(ortutay): I cannot figure out why the commented code below is not
	// working as expected...
	// funcMap := template.FuncMap{}
	// tmpl := template.Must(template.New("/home/marcell/gocode/src/github.com/ortutay/nodez/templates/_base.html").
	// 	Funcs(funcMap).
	// 	ParseFiles(
	// 	"/home/marcell/gocode/src/github.com/ortutay/nodez/templates/_base.html",
	// 	"/home/marcell/gocode/src/github.com/ortutay/nodez/templates/nodez.html"))

	file, err := os.Open("templates/nodez.html")
	if err != nil {
		errorHandler(w, r, err)
		return
	}
	buf, err := ioutil.ReadAll(file)
	if err != nil {
		errorHandler(w, r, err)
		return
	}

	tmpl := template.Must(template.New("nodez").Parse(string(buf)))
	tc := make(map[string]interface{})
	if err := tmpl.Execute(w, tc); err != nil {
		errorHandler(w, r, err)
		return
	}
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	log.Infof("Error: %v\nfor request: %v\n", err, r)
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}

func isDevMode(r *http.Request) bool {
	return r.Host == fmt.Sprintf("localhost:%s", *port)
}

func handleBitcoinStream() {
	// Try to connect to local bitcoin node
	conn, err := net.Dial("tcp", *nodeAddr)
	if err != nil {
		log.Fatal(err)
	}

	pver := wire.ProtocolVersion
	btcnet := wire.MainNet

	// Send version message
	verMsg, err := wire.NewMsgVersionFromConn(conn, 1, 0)
	if err != nil {
		log.Fatal(err)
	}
	if err := wire.WriteMessage(conn, verMsg, pver, btcnet); err != nil {
		log.Fatal(err)
	}
	verResp, verPayload, err := wire.ReadMessage(conn, pver, btcnet)
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("msg: %v, payload: %v\n", verResp, verPayload)

	// Send ping message
	pingMsg := wire.NewMsgPing(123)
	if err := wire.WriteMessage(conn, pingMsg, pver, btcnet); err != nil {
		log.Fatal(err)
	}
	pingResp, pingPayload, err := wire.ReadMessage(conn, pver, btcnet)
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("msg: %v, payload: %v\n", pingResp, pingPayload)

	for {
		msg, _, err := wire.ReadMessage(conn, pver, btcnet)
		if err != nil {
			log.Fatal(err)
		}

		switch msg := msg.(type) {
		case *wire.MsgInv:
			for _, inv := range msg.InvList {
				_ = inv
				log.Infof("%d %s", int(inv.Type), inv.Hash.String())
			}
		default:
			log.Infof("got %v", msg.Command())
		}
	}
}
