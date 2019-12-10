FROM golang:1.13

# local vendor mod
ENV GO111MODULE=off

WORKDIR /go/src/github.com/chenjiandongx/conveyor

# download and extrac filebeat.tar.gz
ARG FILEBEAT_VERSION=7.4.2
RUN wget -O ~/filebeat.tar.gz https://artifacts.elastic.co/downloads/beats/filebeat/filebeat-${FILEBEAT_VERSION}-linux-x86_64.tar.gz
RUN tar -C /etc -xzf ~/filebeat.tar.gz && mv /etc/filebeat-${FILEBEAT_VERSION}-linux-x86_64 /etc/filebeat && mkdir -p /etc/filebeat/configs

# build
ADD . /go/src/github.com/chenjiandongx/conveyor
RUN go build ./main.go && chmod +x ./main

ENTRYPOINT ["./main"]
