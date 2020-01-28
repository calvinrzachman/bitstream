GO111MODULE := on

.PHONY: lightning
lightning:
	docker-compose up

.PHONY: bitstream
bitstream:
	docker-compose up -d
	cd ./lightninginabottle && go run lightining_in_a_bottle.go
	go run main.go

.PHONY: stream
stream:
	cd ./client && go run main.go

.PHONY: clean
clean:
	docker-compose down
	docker volume rm bitstream_bitcoin bitstream_lnd bitstream_shared
	cd config/client/lnd && rm tls.* && rm -r logs
	cd config/server/lnd && rm tls.* && rm -r logs
	cd config/client/ && rm -r macaroons
	cd config/server/ && rm -r macaroons

.PHONY: clean-config
clean-config:
	cd config/client/lnd && rm tls.* && rm -r logs
	cd config/server/lnd && rm tls.* && rm -r logs
	cd config/client/ && rm -r macaroons
	cd config/server/ && rm -r macaroons