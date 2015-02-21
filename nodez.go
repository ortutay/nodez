package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"code.google.com/p/go-uuid/uuid"

	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcrpcclient"
	log "github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var (
	port        = flag.String("port", "8080", "Port to listen on")
	nodeAddr    = flag.String("node_addr", ":8333", "Bitcoin node address")
	bitcoinConf = flag.String("bitcoin_conf", "~/.bitcoin/bitcoin.conf", "Bitcoin configuration file")
	staticDir   = flag.String("static_dir", ".", "Path to static files")
	testnet     = flag.Bool("testnet", false, "Connect to testnet")
)

func main() {
	flag.Parse()

	*bitcoinConf = strings.Replace(*bitcoinConf, "~", os.Getenv("HOME"), -1)

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

	client, err := btcrpcclient.New(connCfg, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Shutdown()

	// Get the current block count.
	// TODO(ortutay): if error, probably syncing
	blockCount, err := client.GetBlockCount()
	if err != nil {
		log.Error(err)
	} else {
		log.Infof("Block count: %v", blockCount)
	}

	var btcnet wire.BitcoinNet
	if *testnet {
		btcnet = wire.TestNet3
	} else {
		btcnet = wire.MainNet
	}
	go handleBitcoinStream(wire.ProtocolVersion, btcnet)

	r := mux.NewRouter()
	mux := http.NewServeMux()

	r.Handle("/nodez/wire/stream", Endpoint{Serve: handleWireStream})
	r.Handle("/nodez", Endpoint{Serve: handleHome})

	mux.Handle("/", r)

	http.Handle("/static/", http.FileServer(http.Dir(*staticDir)))
	http.Handle("/", r)

	log.Infof("Listening at %v...", *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

var msgChans = make(map[string]chan wire.Message)
var msgChansLock sync.Mutex

type WireJSON struct {
	Command string

	// For "inv" message
	Inv []InvJSON `json:",omitempty"`
}

type InvJSON struct {
	Type string
	Hash string
}

func handleWireStream(w http.ResponseWriter, r *http.Request, ctx *Context) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	msgChansLock.Lock()
	id := uuid.New()
	ch := make(chan wire.Message)
	msgChans[id] = ch
	defer func() {
		delete(msgChans, id)
		close(ch)
		conn.Close()
	}()
	msgChansLock.Unlock()

	for {
		msg := <-ch

		var wireMsg WireJSON
		wireMsg.Command = msg.Command()
		switch msg := msg.(type) {
		case *wire.MsgInv:
			for _, inv := range msg.InvList {
				invMsg := InvJSON{}
				invMsg.Hash = inv.Hash.String()

				switch inv.Type {
				case wire.InvTypeError:
					log.Errorf("inv type error: %d", inv.Type)
					continue
				case wire.InvTypeTx:
					invMsg.Type = "tx"
				case wire.InvTypeBlock:
					invMsg.Type = "block"
				case wire.InvTypeFilteredBlock:
					invMsg.Type = "filteredBlock"
				default:
					log.Errorf("unknown inv type: %d", inv.Type)
				}

				wireMsg.Inv = append(wireMsg.Inv, invMsg)
			}
		}

		data, err := json.Marshal(wireMsg)
		if err != nil {
			log.Errorf("couldn't marshal JSON %v: %s", wireMsg, err)
			continue
		}

		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Errorf("couldn't write to websocket: %s", err)
			continue
		}
	}

	return nil
}

func handleHome(w http.ResponseWriter, r *http.Request, ctx *Context) error {
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
		return err
	}
	buf, err := ioutil.ReadAll(file)
	if err != nil {
		return err
	}

	tmpl := template.Must(template.New("nodez").Parse(string(buf)))
	tc := make(map[string]interface{})
	if err := tmpl.Execute(w, tc); err != nil {
		return err
	}

	return nil
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	log.Infof("Error: %v\nfor request: %v\n", err, r)
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}

func isDevMode(r *http.Request) bool {
	return r.Host == fmt.Sprintf("localhost:%s", *port)
}

func handleBitcoinStream(pver uint32, btcnet wire.BitcoinNet) {
	// Try to connect to local bitcoin node
	conn, err := net.Dial("tcp", *nodeAddr)
	if err != nil {
		log.Fatal(err)
	}

	// Send version message
	verMsg, err := wire.NewMsgVersionFromConn(conn, 1, 0)
	if err != nil {
		log.Fatal(err)
	}
	if err := wire.WriteMessage(conn, verMsg, pver, btcnet); err != nil {
		log.Fatal(err)
	}
	_, _, err = wire.ReadMessage(conn, pver, btcnet)
	if err != nil {
		log.Fatal(err)
	}

	for {
		msg, _, err := wire.ReadMessage(conn, pver, btcnet)
		if err != nil {
			log.Fatal(err)
		}

		for _, ch := range msgChans {
			ch <- msg
		}
	}
}
