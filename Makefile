apply-terraform: api.zip
	terraform apply

api.zip: main template.html fasttext
	rm -f api.zip
	zip -9 api.zip main template.html fasttext
	ls -lah api.zip

main: main.go classifier.go
	golint -set_exit_status
	go vet
	GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o main
