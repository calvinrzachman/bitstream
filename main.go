package main

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"time"

	"projects/bitstream/rpc"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"gopkg.in/macaroon.v2"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

/*
	For notes on streaming with the HTTP protocol see: https://stackoverflow.com/questions/48267616/why-is-golang-http-responsewriter-execution-being-delayed
	Without the help from the article above we would likely need to use the net package directly to continuously send data over a TCP connection (TRY THIS!)

	TODO:
	- Create a client which opens connection to the random server and streams data.
	- Setup Lightning Network Functionality:
		- This server will need to be equipped with something like https://github.com/btcsuite/btcd/tree/master/rpcclient so that it may communicate
			with a full-node running Lightning via RPC or localhost
		- Have the server either:
			1. Window Method: Periodically check to see whether it has recieved a Lightning payment from the client (NOTE: this will require a way to ID the client) and only
				continue sending data if it continues to receive payments. Data will be transmitted as fast as HTTP/TCP will allow and hang/stall intermittently waiting on
				successful receipt of payment (htlc to resolve). If no payment has been received in the last X unit of time then stop streaming until payment resumes
			2. Tit for Tat Method: Only send data upon reciept of (NOTE: this method will enforce an upper bound on stream speed - limiting the stream speed to the speed
			of the Lightning network which, despite the name, is not as fast as TCP/HTTP as it requires multiple rounds of communication to negotiate the trustless transfer of
			funds. For this reason we should shoot for the Window method and only do this if it is easier)

			This will involve generating preimages and payment hashes (unless we try spontaneous payments - GOOD IDEA!)
		- Similarly, setup the client to talk to its own Lightning node and generate payments with LND as it recieves data from the server.
		The client will contact lnd to send a lightning payment to the server
		The client will pay with good faith in the server, but will need to check that it continues to receive data.

		What happens if the data/payment is a little late to arrive?

		With the above we hope to acheive a (logical) bidirectional stream of data and money as pictured below:

		                   data
					  		-->
					Server		 Client
							<--
						   crypto

		A bidirectional stream of value

	If both nodes are mine, anyone can demo and I wont lose any money (maybe a tiny amount). The client will need to be rebalanced eventually so perhaps we can send a
	payment back to him every so often.

	The server will have one PaymentProcessor routine which will gather information on lightning network (LN) payments from the LN backend and
	send to the routine processing the user request (NOTE: This will be one payment processor to X handlers if a client makes X requests.
	In the case of multiple clients, the PaymentProcessor will need to send the lightning payments to the correct handler for each client)

	We could either have a case of every request handler reaching out to the LN backend on its own to get transactions which pertain to it
	OR a proxy of types that is the sole interactor with the backend and communicates what it finds to the individual request handlers

	NOTE: We should make use of the context package to clean up go routines because server resources will otherwise be wasted should connections close
	NOTE: Method 1 (adapted HTTP) might be required for this to be used in a browser
	Look at the etcd Watch API (Watcher RPCs) - https://github.com/etcd-io/etcd/blob/877aa2497e3fe1fea3c673bc8a84abb64619b8a6/etcdserver/api/v3rpc/watch.go
	sendLoop() and recvLoop(), gRPC streams and Watch_WatchServer

	Learn about gRPC and Protocol Buffers (start with justForFunc)
	Learn about WebSockets

	QUESTION: Should I multiplex client handler requests over same gRPC/TCP connection to lnd?
	Or should each client handler open its own connection to lnd? Should we poll the lnd backend?

	We now have a scenario where the Bitstream server wishes to stream both random bytes AND
	Lightning Network invoices to the client. The client will pay to these invoices (Payment Stream)

	Learn about Spontaneous payments

	Create an API with micropaywalled resources
		- Server returns 402 Payment Required along with half baked macaroon and signed invoice
		- Client pays to invoice and finishes macaroon bake
		- Client reissues request and now has (revokable) access to resource
	LSAT (Lightning Service Authentication Token)

	IMPORTANT: After integrating Lightning Network functionality the server will no longer run
	on computers without the proper LND setup. Either containerize/script LN Dev setup so that
	Bitstream can run as a standalone or enable the server to plugin to nodes running on machines
	more generally

	IMPORTANT: Look into differences between SendPayment and SendPaymentSync RPC

	NOTE: RouterRPC TrackPayment RPC seems built for precisely this purpose. Use to verify payment settlement
	inside of StreamPayment

	ALTERNATIVE METHOD: From main create and run go BitstreamServer() and treat this as
	new main go routine for each client connection, inside which we run go streamRandomBytes and stream
	payment requests. This might simplify the code.

	Other Links:
	- https://medium.com/@nate510/don-t-use-go-s-default-http-client-4804cb19f779
	- https://medium.com/statuscode/how-i-write-go-http-services-after-seven-years-37c208122831

	- https://stackoverflow.com/questions/11104085/in-go-does-a-break-statement-break-from-a-switch-select
	- https://stackoverflow.com/questions/35445630/how-does-this-for-loop-break-when-select-is-done
	- https://stackoverflow.com/questions/21783333/how-to-break-out-of-select-gracefuly-in-golang
*/

var (
	byteStreamDelay time.Duration = 100 * time.Millisecond
	paymentWindow   time.Duration = 10 * time.Second
	pauseTimeout    time.Duration = 5 * time.Second
	lndPollInterval time.Duration = 2 * time.Second // Wait time between successful polls of LND backend for given client
	// Do we need a poll interval?
	payments PaymentProcessor
)

const bitstreamRPCPort = "10009"

func init() {
	// Initialize the Payment Processor
	payments = PaymentProcessor{
		Notifiers: map[string]*Subscriber{},
	}

	err := payments.EnableLightning(bitstreamRPCPort)
	if err != nil {
		log.Fatalf("[PaymentProcessor init]: failed to setup payment processor - %v", err)
	}
}

func main() {
	// METHOD 1: HTTP Protocol
	log.Println("Ready to serve...")
	// http.HandleFunc("/", handler)
	// http.ListenAndServe("127.0.0.1:8080", nil)

	// METHOD 2: Layer 4 - Direct TCP
	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal(err)
	}

	// METHOD 3: WebSockets (HTTP falls back to raw TCP connection and keeps it open)
	// METHOD 4: gRPC (Create a unidirectional stream of data server -> client)
	// Since we're looking to stream both random bytes AND invoices this might be whats needed

	// NOTE: LND -> Bitstream already uses gRPC

	// Start the PaymentProcessor routine which will gather information on lightning network (LN)
	// payments from the LN backend and communicate what it finds to the individual request handlers
	go payments.Process()

	for { // Serve connections continuously for the lifetime of the server
		conn, err := l.Accept() // Blocks awaiting a TCP connection
		if err != nil {
			log.Fatal(err)
		}

		// Service the connection - generally: go serviceConn(conn)
		go streamRandomBytes(conn)
		// NOTE: Do not block anywhere else in this loop
	}
}

// streamRandomBytes streams random bytes to the Bitstream client
func streamRandomBytes(conn net.Conn) {
	log.Printf("[%s - handler routine]: Streaming random bytes...", conn.RemoteAddr().String())
	defer conn.Close()

	lightningChannel := make(chan *LightningPayment)
	lastPayment := &LightningPayment{Received: time.Now()}

	// Register with the PaymentProcessor
	requestID := conn.RemoteAddr().String()
	payments.Register(requestID, lightningChannel) // receive updates on payments for this client
	defer log.Printf("[%s - handler routine]: Finished handling requests for this client!", requestID)

Stream:
	for {
		select {
		case lastPayment, _ = <-lightningChannel:
			log.Printf("[%s - handler routine]: Payment received!", requestID)
		// TODO: Context/Cancellation case
		default: // A default case allows for a non-blocking channel read
			// log.Println("No payment received. Hopefully we get one soon!")
		}

		if time.Since(lastPayment.Received) > paymentWindow {
			log.Printf("[%s - handler routine]: Failed to receive payment in last %s. Holding stream while awaiting payment...", requestID, paymentWindow.String())
			select { // Pause/Resume mechanism
			case lastPayment, _ = <-lightningChannel:
				log.Printf("[%s - handler routine]: Payment received! Resuming stream", requestID)
			case <-time.After(pauseTimeout):
				log.Printf("[%s - handler routine]: Ending stream. Did not receive payment within timeout.", requestID)
				break Stream
			}
		}

		WriteRandomBytes(conn, 1)
		time.Sleep(byteStreamDelay)
	}
}

// NOTE: A for loop containing a select statment without a default case will block and only run as often as channel updates
// are received. A simple use of time.After() would be to rate limit/dictate how often the for loop executes (this would be equivalent to a time.Sleep()
// in the for loop body)

/*
	IDEA: Separate loop which writes random bytes and the loop which checks for payment.
*/

// WriteRandomBytes writes n random bytes to a writer
func WriteRandomBytes(w io.Writer, n int) []byte {
	bytes, _ := GenerateRandomBytes(n)
	fmt.Fprintf(w, "%v", bytes)
	return bytes
}

// GenerateRandomBytes returns securely generated random bytes.
// It will return an error if the system's secure random
// number generator fails to function correctly, in which
// case the caller should not continue.
func GenerateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	// Note that err == nil only if we read len(b) bytes.
	if err != nil {
		return nil, err
	}

	return b, nil
}

/*

	LIGHTNING NETWORK

*/

// LightningPayment represents a Bitcoin payment received over the lightning network
type LightningPayment struct {
	ID string
	*lnrpc.Invoice
	Received time.Time
}

// Subscriber represents a handler go routine registered to recieve payment updates
type Subscriber struct {
	ID         string
	Notifier   chan *LightningPayment
	Registered bool
	// PaymentRequestServer rpc.Payments_StreamPaymentRequestsServer
}

// PaymentProcessor gathers and handles payments from a Lightning Network
// backend on behalf of the Bitstream server
type PaymentProcessor struct {
	Notifiers       map[string]*Subscriber // NOTE: vanilla maps are not safe for current use.
	LightningClient lnrpc.LightningClient
}

// Process starts Bitstream gRPC server for streaming payment requests to client.
// Formerly used channels to construct control loop for payment processing.
// By the proverbial "sharing by communicating", we fetch lightning payments from the Lightning Network backend (LND)
// asynchronously and notify the various client request handlers when payments have been received
// NOTE(It would be cool if this could be implemented with a watch of the DB like controllers in K8s)
func (p *PaymentProcessor) Process() {
	log.Println("[Payment Processor routine]: Awaiting subscribers to process!")
	defer log.Println("[Payment Processor routine]: Completed all Processing!")

	// Setup gRPC Payment Request streaming server
	srv := grpc.NewServer()

	rpc.RegisterPaymentsServer(srv, p)
	l, err := net.Listen("tcp", ":8081")
	if err != nil {
		log.Fatalf("[Payment Processor routine]: could not listen on :8081: %v", err)
	} else {
		log.Println("[Payment Processor routine]: listening on :8081")
	}
	srv.Serve(l)
}

// NOTE: It might be more efficient to have the Processor open long running go routines for each client
// which poll lnd in a loop

// AddInvoice creates a new invoice for the provided amount
// TODO: propogate context from StreamPaymentRequests RPC
func (p *PaymentProcessor) AddInvoice(amt int64, ID string) (*lnrpc.Invoice, error) {
	log.Printf("[%s - Payment Processor AddInvoice]: Adding Invoice!", ID)
	ctx := context.Background()

	// Generate payment invoice
	i := &lnrpc.Invoice{Value: amt}
	addInvoiceResponse, err := p.LightningClient.AddInvoice(ctx, i)
	if err != nil {
		return nil, fmt.Errorf("unable to add invoice: %v", err)
	}
	log.Printf("[%s - Payment Processor AddInvoice]: Successfully created invoice: %x", ID, addInvoiceResponse.RHash[:6])

	invoice := &lnrpc.Invoice{}
	invoice.PaymentRequest = addInvoiceResponse.PaymentRequest
	invoice.RHash = addInvoiceResponse.RHash
	return invoice, nil
}

// VerifyInvoice verifies that an invoice has been settled
func (p *PaymentProcessor) VerifyInvoice(hash *lnrpc.PaymentHash, ID string) (*lnrpc.Invoice, error) {
	log.Printf("[%s - Payment Processor VerifyInvoice]: Verifying Invoice - %x", ID, hash.RHash[:6])
	ctx := context.Background()

	// Lookup invoice
	invoice, err := p.LightningClient.LookupInvoice(ctx, &lnrpc.PaymentHash{RHash: hash.RHash})
	if err != nil {
		return nil, fmt.Errorf("unable to lookup invoice: %v", err)
	}

	log.Printf("[%s - Payment Processor VerifyInvoice]: Current Status: %s\tValue: %d\t AmountPaid: %d", ID, invoice.State.String(), invoice.Value, invoice.AmtPaidSat)
	return invoice, nil
}

// Register a client request handler with the PaymentProcessor
func (p *PaymentProcessor) Register(id string, lightningChannel chan *LightningPayment) {
	log.Printf("[%s - Payment Processor]: Registering client!", id)
	s := &Subscriber{ID: id, Notifier: lightningChannel, Registered: true}

	// Add subscriber to ID map
	p.Notifiers[id] = s // NOTE: This is concurrent access to the Notifier map.
}

// StreamPaymentRequests RPC creates a unidirectional stream of Lightning Network
// invoices from Server -> Client. A Verification go routine monitors successfull settlement
// of payment requests and notifies the RandomByte streamer upon receipt of each payment.
func (p *PaymentProcessor) StreamPaymentRequests(req *rpc.StreamPaymentRequest, updateStream rpc.Payments_StreamPaymentRequestsServer) error {
	log.Printf("[%s - inside StreamPaymentRequests]: Streaming payment requests to client!", req.ClientID)
	ctx := updateStream.Context()
	invoiceChan := make(chan *lnrpc.Invoice) // Buffered channel
	sub, ok := p.Notifiers[req.ClientID]
	if !ok {
		log.Printf("[%s - inside StreamPaymentRequests]: No notifier in map!", req.ClientID)
	}

	// Verify outstanding invoices. NOTE: We create an invoice and then send
	// it to the client. We then need to periodically check that the invoice is paid.
	var invoiceToVerify *lnrpc.Invoice
	go func() {
		for {
			select {
			case invoiceToVerify = <-invoiceChan: // We have a new invoice to verify settlement
			case <-time.After(lndPollInterval):
				log.Printf("[%s - inside StreamPaymentRequests Verifier]: Verifying settlement of invoice ... %x", req.ClientID, invoiceToVerify.RHash[:6])
				invoice, err := p.VerifyInvoice(&lnrpc.PaymentHash{RHash: invoiceToVerify.RHash}, req.ClientID)
				if err != nil {
					log.Printf("[%s - inside StreamPaymentRequests]: unable to verify invoice this go around: %v Trying again ...", req.ClientID, err)
				}

				if invoice.State == lnrpc.Invoice_SETTLED {
					log.Printf("[%s - inside StreamPaymentRequests Verifier]: Invoice settled successfully at %v ! ", req.ClientID, time.Unix(invoice.SettleDate, 0))
					invoiceChan <- invoice // Tell stream to send another payment request
					sub.Notifier <- &LightningPayment{ID: string(invoice.RHash), Invoice: invoice, Received: time.Unix(invoice.SettleDate, 0)}
				}
			case <-ctx.Done():
				log.Printf("[%s - inside StreamPaymentRequests]: Verification complete. Context cancelled!", req.ClientID)
				return
			}
		}
	}()

	// Generate and send the first invoice
	invoice, err := p.AddInvoice(125, req.ClientID)
	if err != nil {
		return fmt.Errorf("unable to add invoice: %v", err)
	}
	request := rpc.PaymentRequest{PaymentRequest: invoice.PaymentRequest}
	_ = updateStream.Send(&request)
	invoiceChan <- invoice

	// Stream payment requests as they are paid or until told to stop
	for {
		select {
		case <-invoiceChan: // Write to this channel ONLY when we recieve payment.
			// This way we only generate and send a new invoice to the client after the previous settles
			invoice, err := p.AddInvoice(125, req.ClientID)
			if err != nil {
				return fmt.Errorf("unable to add invoice: %v", err)
			}
			request := rpc.PaymentRequest{PaymentRequest: invoice.PaymentRequest}
			err = updateStream.Send(&request)
			if err != nil {
				return fmt.Errorf("unable to send invoice to Bitstream client: %v", err)
			}
			invoiceChan <- invoice
		case <-ctx.Done():
			log.Printf("[%s - inside StreamPaymentRequests]: Closing stream. Context cancelled!", req.ClientID)
			return nil
			// case <-p.quit:
			// 	log.Printf("[%s - inside StreamPaymentRequests]: Ending invoice stream!", req.ClientID)
			// 	return nil
		}

	}
}

// EnableLightning opens a gRPC connection to lightning network daemon backend
// TODO: If this function is to be reused across client/server maybe make it take
// the gRPC server port of lnd as a parameter
func (p *PaymentProcessor) EnableLightning(rpcPort string) error {

	tlsCertPath := "./config/server/lnd/tls.cert"
	macaroonPath := "./config/server/macaroons/admin.macaroon"

	tlsCreds, err := credentials.NewClientTLSFromFile(tlsCertPath, "")
	if err != nil {
		return fmt.Errorf("Cannot get node tls credentials: %v", err)
	}

	macaroonBytes, err := ioutil.ReadFile(macaroonPath)
	if err != nil {
		return fmt.Errorf("Cannot read macaroon file: %v", err)
	}

	mac := &macaroon.Macaroon{}
	if err = mac.UnmarshalBinary(macaroonBytes); err != nil {
		return fmt.Errorf("Cannot unmarshal macaroon: %v", err)
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsCreds),
		grpc.WithBlock(),
		grpc.WithPerRPCCredentials(macaroons.NewMacaroonCredential(mac)),
	}

	conn, err := grpc.Dial("localhost:"+rpcPort, opts...)
	if err != nil {
		return fmt.Errorf("cannot dial lnd backend: %v", err)
	}

	p.LightningClient = lnrpc.NewLightningClient(conn)
	return nil
}
