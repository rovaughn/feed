main: *.go
	go vet
	go build -o $@

fastText-0.1.0.zip:
	wget https://github.com/facebookresearch/fastText/archive/v0.1.0.zip -O $@

fastText-0.1.0: fastText-0.1.0.zip
	unzip $^

fastText-0.1.0/fasttext: fastText-0.1.0
	make -C fastText-0.1.0

fasttext: fastText-0.1.0/fasttext
	cp $^ $@
