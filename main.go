package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"sync"
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
)

const demoPaymentID string = "1"

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

	// Start the PaymentProcessor routine which will gather information on lightning network (LN)
	// payments from the LN backend and communicates what it finds to the individual request handlers
	payments := &PaymentProcessor{Notifiers: map[string]*Subscriber{}}
	go payments.Process()

	for { // Serve connections continuously for the lifetime of the server
		conn, err := l.Accept() // Blocks awaiting a TCP connection
		if err != nil {
			log.Fatal(err)
		}

		// Service the connection - generally: go serviceConn(conn)
		go streamRandomBytes(conn, payments)
		// NOTE: Do not block anywhere else in this loop
	}
}

// streamRandomBytes reimplements 'handler' using the net package directly
// in an attempt to create an actual stream of bytes to the client
func streamRandomBytes(conn net.Conn, paymentProcessor *PaymentProcessor) {
	log.Printf("Streaming random bytes for client: %s ...", conn.RemoteAddr().String())
	defer conn.Close() // IMPORTANT: Connections must be closed otherwise they will hang around in CLOSE_WAIT

	lightningChannel := make(chan *LightningPayment)
	lastPayment := &LightningPayment{Received: time.Now()}

	// Register with the PaymentProcessor
	requestID := conn.RemoteAddr().String()
	paymentProcessor.Register(requestID, lightningChannel) // receive updates on payments for this client

Stream:
	for {
		// log.Println(("Another stream cycle!"))
		select {
		case lastPayment, _ = <-lightningChannel:
			log.Println("Payment received! Client ID: ", requestID)
		// TODO: Context/Cancellation case
		default: // A default case allows for a non-blocking channel read
			// log.Println("No payment received. Hopefully we get one soon!")
		}

		if time.Since(lastPayment.Received) > paymentWindow {
			log.Printf("Failed to receive payment from %s in last %s. Holding stream while awaiting payment...", requestID, paymentWindow.String())
			select { // Pause/Resume mechanism
			case lastPayment, _ = <-lightningChannel: // Resume the stream upon receipt of payment
				log.Println("Payment received! Resuming stream for Client ID: ", requestID)
			case <-time.After(pauseTimeout):
				log.Println("Ending stream. Did not receive payment within timeout.")
				paymentProcessor.Unregister(requestID)
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
	ID       string
	Notifier chan *LightningPayment
}

// PaymentProcessor gathers and handles payments from a Lightning Network backend on behalf of the server
type PaymentProcessor struct {
	Payments    []*LightningPayment
	Subscribers []*Subscriber // A subscriber carries its own notifier channel (Do we need both this and the next thing?)
	// Notifiers   map[string]chan *LightningPayment // Map from client ID to payment notifier channel
	Notifiers map[string]*Subscriber // NOTE: vanilla maps are not safe for current use. Are we accessing this concurrently? I dont think so
	Backend   *net.Conn
}

// Fetch obtains payments from Lightning Network backend
func (p PaymentProcessor) Fetch(backend *net.Conn, id string) ([]*LightningPayment, error) {
	log.Printf("Fetching payments from lnd for client ID: %s ...", id)
	var payments []*LightningPayment

	// Use a lnd RPC client to make a request for payments to local running instance of lnd

	// Simulate fetching of payments
	rand.Seed(time.Now().UnixNano())
	n := rand.Intn(10)
	time.Sleep(time.Duration(n) * time.Second) // Sometimes it takes longer than others
	if n < 8 {
		log.Printf("Successfully received payment for client ID: %s ...", id)
		payments = append(payments, &LightningPayment{ID: id, Received: time.Now()})
	}

	return payments, nil
}

// POSSIBLE REDESIGN: Should the Processor fetch all new payments from lnd and then process locally OR should it reach out to lnd
// on behalf of each client handler specifically? If change, dont iterate over subscribers below

// Process fetches lightning payments from the Lightning Network backend (LND) and
// notifies the various client request handlers when payments have been received
// NOTE(It would be cool if this could be implemented with a watch of the DB like controllers in K8s)
func (p *PaymentProcessor) Process() {
	for {
		log.Println("Processing lightning payments ...")
		if len(p.Subscribers) == 0 {
			log.Println("No subscribers to monitor payments for!")
			time.Sleep(4 * time.Second)
			continue
		}

		for _, sub := range p.Subscribers {
			// Check with lnd to see if we have recieved payment for a given client
			payments, _ := p.Fetch(p.Backend, sub.ID) // NOTE: This code is currently blocking
			// backendChannel := make(chan *LightningPayment)
			// go p.Fetch(p.Backend, sub.ID, backendChannel)

			// If payment has been received, send to the appropriate channel
			for _, payment := range payments {
				log.Printf("Sending payment for client ID: %s", payment.ID)
				// select {
				// case <-time.After(2 * time.Second):
				// case p.Notifiers[payment.ID].Notifier <- payment:
				// }
				// go func(payment *LightningPayment) {
				// 	log.Println("[Sender Goroutine]: Sending Payment")
				p.Notifiers[payment.ID].Notifier <- payment // What happens if this blocks? The channel is individual to the handler routine
				// If it blocks here, the whole Payment Processor is blocked. If it blocks in a go routine then it will sit there forever unless given a way to be cancelled
				// NOTE: The above is not safe and can lead to a nil pointer dereference in the event where the subscriber
				// 	log.Println("[Sender Goroutine]: Payment Sent!")
				// }(payment)
			}
		}

		time.Sleep(2 * time.Second)
	}
}

// NOTE: Could even consider this a "Monitor" routine and continuously monitor lnd for payments
// NOTE: The Process() function above is currently plagued by the fact that writes to the handler channels are blocking. Figure out how to
// unblock these writes non-block writes (tho this might risk not notifying the handler), a "Monitor", or maybe just removing the processor.
// In addition, it is synchronous. This has the effect that fetching payments and notifying one request handler comes at the expense of another

// Register a client request handler with the PaymentProcessor
func (p *PaymentProcessor) Register(id string, lightningChannel chan *LightningPayment) {
	log.Println("Registering client ID: ", id)
	s := &Subscriber{ID: id, Notifier: lightningChannel}

	// Add to list of subscribers
	p.Subscribers = append(p.Subscribers, s)
	log.Println("Subscribers: ", p.Subscribers, len(p.Subscribers))

	// Add subscriber to ID map
	p.Notifiers[id] = s
}

// Unregister removes a client request handler from the PaymentProcessor
func (p *PaymentProcessor) Unregister(id string) error {
	// Find the index of the subscriber in the array
	var index int = -1
	for i, sub := range p.Subscribers {
		if id == sub.ID {
			index = i
		}
	}

	if index == -1 {
		return errors.New("This id is not registered with Payment Processor")
	}

	s := p.Subscribers
	s[len(s)-1], s[index] = s[index], s[len(s)-1]
	s = s[:len(s)-1]
	p.Subscribers = s
	// p.Notifiers[id] = nil // Is this safe?
	return nil
}

// CrunchNumbers creates a go routine that generates random bytes and writes them to a writer
func CrunchNumbers(w http.ResponseWriter, wg *sync.WaitGroup) chan []byte {
	c := make(chan []byte)
	go func(w http.ResponseWriter) {
		defer wg.Done()
		defer close(c)
		for i := 0; i < 5; i++ {
			c <- WriteRandomBytes(w, 1) // Generate random bytes and write to channel
			time.Sleep(250 * time.Millisecond)
		}
	}(w)
	return c // Return channel to main go routine so it can read from it for logging

}

func handler(w http.ResponseWriter, r *http.Request) {
	// ctx := r.Context()

	log.Println("Handler Started...") // Try using a decorator instead
	defer log.Println("Handler Ended")

	var wg sync.WaitGroup
	wg.Add(1)

	// Number Cruncher GoRoutine
	c := CrunchNumbers(w, &wg)
	// c := func(w http.ResponseWriter) chan []byte { // Alternate method: inline function
	// 	c := make(chan []byte)
	// 	go func() {
	// 		defer wg.Done()
	// 		defer close(c)
	// 		for i := 0; i < 5; i++ {
	// 			c <- WriteRandomBytes(w, 1) // Generate random bytes and write to channel
	// 		}
	// 	}()
	// 	return c // Return channel to main go routine so it can read from it for logging
	// }(w)

	// Read from the channel
	// var item []byte
	// var ok bool
	for item := range c {
		log.Println("Wrote to socket", item)
	}
	// OuterLoop: // Alternative method: for/select
	// 	for {
	// 		select {
	// 		case item, ok = <-c:
	// 			if !ok {
	// 				log.Println("Channel has closed!") // There is nothing left to read from the channel (does this make the next case redundant?)
	// 				break OuterLoop
	// 			}
	// 			log.Println("Wrote to socket", item)
	// 			// c = nil - reads from nil channels block forever
	// 		case <-time.After(5 * time.Second):
	// 			// fmt.Fprintln(w, "I have mercy!")
	// 			log.Println("I have mercy!")
	// 			break OuterLoop
	// 			// default:
	// 			// 	time.Sleep(1 * time.Second)
	// 			// 	log.Println("Nothing to process...")
	// 			// case <-ctx.Done():
	// 			// 	err := ctx.Err()
	// 			// 	log.Println(err.Error())
	// 			// 	break
	// 			// http.Error(w, err.Error(), http.StatusInternalServerError)
	// 		}
	// 	}

	wg.Wait()
}

// close(lightningChannel) // Close channel so PaymentProcessor knows he can wrap up - WRONG writes to a closed channel panic. Need another way
// case <-ctx.Done(): // TODO: Context/Cancellation case
// 	err := ctx.Err()
// 	log.Println(err.Error())
// 	break PaymentProcessor/Streamer

// lightningChannel := make(chan *LightningPayment)
// select {
// case lastPayment := <-lightningChannel:
// 	// lastPayment := getLastPayment()
// 	if time.Since(lastPayment.Received) > 10*time.Second {
// 		log.Println("Where is my money at?")
// 	}

// default:
// 	WriteRandomBytes(conn, 1)
// }
