export TLS_CERT_FILE=$PWD/certs/webhook-crt.pem
export TLS_KEY_FILE=$PWD/certs/webhook-key.pem

go build -o bin/webhook src/main.go && ./bin/webhook