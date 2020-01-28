# bitstream
a simple Lightning Network enabled bit(coin) streamer

## Run locally

Setup a containerized lightning network development environment and deploy the Bitstream server with:
    
    make bitstream

Stream data from the server in exchange for lightning payments by running (in a separate window):
    
    make stream
  
 NOTE: The Lightning Network containers mount files to the workspace on your computer. Cleanup the workspace with `make clean` when finished. This will clear out Docker volumes and ensure that `make bitstream` will continue to function.
