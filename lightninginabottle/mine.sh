#!/bin/bash
# Mine the specified number of blocks

# $1 - Number of blocks to be mined
# $2 - Mining Address
set +e

number_of_blocks=$1
echo "Creating ${number_of_blocks} blocks with rewards going to $2!"

# Recreate bitstream-btcd node and set mining address:
MINING_ADDRESS=$2 docker-compose up -d bitstream-btcd

# Instruct btcd container to generate new blocks
docker exec bitstream-btcd /start-btcctl.sh generate ${number_of_blocks}
