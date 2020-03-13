# bitstream
a simple Lightning Network enabled bit(coin) streamer

A server streams random bytes and lightning network payment requests to connecting clients. Streams continue so long as clients continue to satisfy requests for payment. 

- Pause/Resume mechanism for streaming should payments not arrive within configurable window 
- StreamPaymentRequests RPC creates a unidirectional stream of Lightning Network invoices from Server -> Client. A Verification go routine monitors successfull settlement of payment requests and notifies the RandomByte streamer upon receipt of each payment.

## Run locally

Setup a containerized lightning network development environment and deploy the Bitstream server with:
    
    make bitstream

Stream data from the server in exchange for lightning payments by running (in a separate window):
    
    make stream
  
 NOTE: The Lightning Network containers mount files to the workspace on your computer. Cleanup the workspace with `make clean` when finished. This will clear out Docker volumes and ensure that `make bitstream` will continue to function.

NOTE: This project makes use of Docker to set up a containerized Lightning Network development environment. A version of the program which simulates fetching lightning network payments can be run as a standalone Golang program and is located in `/historic/pre-lightning`. Start the TCP server and then connect using something like `nc localhost 8080`
