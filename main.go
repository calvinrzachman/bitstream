package main

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"time"
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

	Other Links:
	- https://medium.com/@nate510/don-t-use-go-s-default-http-client-4804cb19f779
	- https://medium.com/statuscode/how-i-write-go-http-services-after-seven-years-37c208122831

	- https://stackoverflow.com/questions/11104085/in-go-does-a-break-statement-break-from-a-switch-select
	- https://stackoverflow.com/questions/35445630/how-does-this-for-loop-break-when-select-is-done
	- https://stackoverflow.com/questions/21783333/how-to-break-out-of-select-gracefuly-in-golang
*/

var (
	paymentWindow time.Duration = 10 * time.Second
	pauseTimeout  time.Duration = 5 * time.Second
	pollInterval  time.Duration = 1 * time.Second // Wait time between successful polls of LND backend for given client
	// Do we need a poll interval?
	payments PaymentProcessor
)

func init() {
	pending, complete := make(chan *Subscriber), make(chan *Subscriber)
	payments = PaymentProcessor{Notifiers: map[string]*Subscriber{}, Pending: pending, Complete: complete}
}

func main() {
	// METHOD 1: HTTP Protocol
	log.Println("Ready to serve...")
	// http.HandleFunc("/", handler)
	// http.ListenAndServe("127.0.0.1:8080", nil)

	// METHOD 2: Layer 4 - Direct TCP
	l, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		log.Fatal(err)
	}

	// METHOD 3: WebSockets (HTTP falls back to raw TCP connection and keeps it open)

	// Start the PaymentProcessor routine which will gather information on lightning network (LN)
	// payments from the LN backend and communicates what it finds to the individual request handlers
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

// streamRandomBytes reimplements 'handler' using the net package directly
// in an attempt to create an actual stream of bytes to the client
func streamRandomBytes(conn net.Conn) {
	log.Printf("[%s - handler routine]: Streaming random bytes...", conn.RemoteAddr().String())
	defer conn.Close() // IMPORTANT: Connections must be closed otherwise they will hang around in CLOSE_WAIT

	lightningChannel := make(chan *LightningPayment)
	lastPayment := &LightningPayment{Received: time.Now()}

	// Register with the PaymentProcessor
	requestID := conn.RemoteAddr().String()
	payments.Register(requestID, lightningChannel) // receive updates on payments for this client
	defer log.Printf("[%s - handler routine]: Finished handling requests for this client!", requestID)

Stream:
	for {
		// log.Println(("Another stream cycle!"))
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
			case lastPayment, _ = <-lightningChannel: // Resume the stream upon receipt of payment
				log.Printf("[%s - handler routine]: Payment received! Resuming stream", requestID)
			case <-time.After(pauseTimeout):
				log.Printf("[%s - handler routine]: Ending stream. Did not receive payment within timeout.", requestID)
				// ctx.Done()
				break Stream // Discontinue streaming random bytes - return or break and sleep for some period of time.
			}
		}

		WriteRandomBytes(conn, 1)
		time.Sleep(250 * time.Millisecond)
	}
}

// NOTE: A for loop containing a select statment without a default case will block and only run as often as channel updates
// are received. A simple use of time.After() would be to rate limit/dictate how often the for loop executes (this would be equivalent to a time.Sleep()
// in the for loop body)

/*
	IDEA: Separate loop which writes random bytes and the loop which checks for payment.
	Create a Watcher loop which sits blocking in wait for either a timer to expire (paymentWindow) or a payment to come
	through (reset this timer) and a Writer loop which continously writes bytes (rate limit if you want) unless it receives an interrupt
	signal from the Watcher routine.

	OR just go with the original way of time.Since(lastPayment.Received) > paymentWindow and a sleep in the loop body
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
	ID       string
	Received time.Time
}

// Subscriber represents a handler go routine registered to recieve payment updates
type Subscriber struct {
	ID         string
	Notifier   chan *LightningPayment
	Registered bool
}

// PaymentProcessor gathers and handles payments from a Lightning Network backend on behalf of the server
type PaymentProcessor struct {
	Pending   chan *Subscriber
	Complete  chan *Subscriber
	Notifiers map[string]*Subscriber // NOTE: vanilla maps are not safe for current use. Are we accessing this concurrently? I dont think so
	Backend   *net.Conn
}

// Fetch obtains payments from Lightning Network backend
func (s Subscriber) Fetch() ([]*LightningPayment, error) {
	log.Printf("[%s - Fetch]: Fetching payments from lnd...", s.ID)
	var payments []*LightningPayment

	// Use a lnd RPC client to make a request for payments to local running instance of lnd

	// Simulate fetching of payments
	rand.Seed(time.Now().UnixNano())
	n := rand.Intn(10)
	time.Sleep(time.Duration(n) * time.Second) // Sometimes it takes longer than others
	if n < 8 {
		log.Printf("[%s - Fetch]: Successfully received payment! Sending to client handler... %d", s.ID, n)
		payments = append(payments, &LightningPayment{ID: s.ID, Received: time.Now()})
	} else {
		log.Printf("[%s - Fetch]: Failed to received payment :( ... %d", s.ID, n)
	}

	return payments, nil
}

// POSSIBLE REDESIGN: Should the Processor fetch all new payments from lnd and then process locally OR should it reach out to lnd
// on behalf of each client handler specifically? If change, dont iterate over subscribers below

// Process uses channels to construct control loop for payment processing.
// By the proverbial "sharing by communicating", we fetch lightning payments from the Lightning Network backend (LND)
// asynchronously and notify the various client request handlers when payments have been received
// NOTE(It would be cool if this could be implemented with a watch of the DB like controllers in K8s)
func (p *PaymentProcessor) Process() {
	log.Println("[Payment Processor routine]: Awaiting subscribers to process!")
	defer log.Println("[Payment Processor routine]: Completed all Processing!")

	// Check with lnd to see if we have recieved payment for a given client asynchronously.
	// NOTE: Each call to Register() adds data to the processing queue so this can only create
	// as many go routines as there are subscribers to the Payment Processor.
	go func() {
		for sub := range p.Pending {
			// Sends subscribers that have finished processing to 'Completed' workload channel
			go sub.Process(p.Complete)
		}
	}()

	// Receive subscribers and return them to the queue of workload for next round of processing
	for sub := range p.Complete {
		go sub.Sleep(p.Pending)
	}
}

// Process fetches lightning payments from the Lightning Network backend (LND) asynchronously
// and notifies the various client request handlers when payments have been received, sending
// the subscriber to 'done' channel to prepare for next round of processing
func (s *Subscriber) Process(done chan<- *Subscriber) {
	defer log.Printf("[%s - processor go routine]: Shutting down", s.ID)

	if !s.Registered { // Remove the subscriber from processing
		log.Printf("[%s - processor go routine]: Client is no longer registered", s.ID)
		return
	}

	payments, _ := s.Fetch()
	for _, payment := range payments {
		select {
		case s.Notifier <- payment: // If the client handler has shut down this will block until the timeout (below) removes the subscriber from rotation
			log.Printf("[%s - processor go routine]: Sending payment to client handler!", s.ID)
		case <-time.After(10 * time.Second): // Ensure go routine does not block awaiting channel write forever (Could this remove sub from rotation when we still want to poll?)
			log.Printf("[%s - processor go routine]: Unable to send payment to client handler - blocked for 10s!", s.ID)
			return
		}
	}
	done <- s // Processing of this subscriber is complete - return to processing queue
}

// Sleep sleeps for a specified interval before sending
// the Subscriber back to the processing channel for continued polling.
func (s *Subscriber) Sleep(done chan<- *Subscriber) {
	log.Printf("[%s - Sleep]: Waiting pollInterval seconds before returning client to input queue", s.ID)
	time.Sleep(pollInterval)
	done <- s
}

// NOTE: Could even consider this a "Monitor" routine and continuously monitor lnd for payments (THIS MIGHT BE IMPORTANT - we may need to share by communicating)
// NOTE: The Process() function above is currently plagued by the fact that writes to the handler channels are blocking. Figure out how to
// unblock these writes non-block writes (tho this might risk not notifying the handler), a "Monitor", or maybe just removing the processor.

// Register a client request handler with the PaymentProcessor
func (p *PaymentProcessor) Register(id string, lightningChannel chan *LightningPayment) {
	log.Println("[Payment Processor]: Registering client ID: ", id)
	s := &Subscriber{ID: id, Notifier: lightningChannel, Registered: true}

	// Send subscriber to Payment Processor queue
	p.Pending <- s
}
