#!/bin/bash -xe

cd /code
go install -v storj.io/gateway-mt/cmd/linksharing

linksharing run \
    --auth-service.base-url http://authservice:8000 \
    --auth-service.token super-secret \
    --address=:8001 \
    --public-url=http://localhost:8001 \
    --log.level=debug
