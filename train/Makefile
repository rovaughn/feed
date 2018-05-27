.PHONY: clean

train: main.go
	go build -o $@

wiki-news-300d-1M.vec.zip:
	wget https://s3-us-west-1.amazonaws.com/fasttext-vectors/wiki-news-300d-1M.vec.zip -O $@

wiki-news-300d-1M.vec: wiki-news-300d-1M.vec.zip
	unzip $^
	touch $@

fastText-0.1.0.zip:
	wget https://github.com/facebookresearch/fastText/archive/v0.1.0.zip -O $@

fastText-0.1.0: fastText-0.1.0.zip
	unzip $^

fastText-0.1.0/fasttext: fastText-0.1.0
	make -C fastText-0.1.0

fasttext: fastText-0.1.0/fasttext
	cp $^ $@

clean:
	rm -rf \
		fastText-0.1.0 \
		fastText-0.1.0.zip \
		fasttext \
		wiki-news-300d-1M.vec \
		wiki-news-300d-1M.vec.zip
