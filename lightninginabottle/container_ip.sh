#!/bin/bash

ip=$(docker inspect bitstream-client | grep 'IPAddress": "1' | awk '{print $2}' | tr -d '"' | tr -d ',')
echo ${ip}