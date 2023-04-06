FROM golang as builder

WORKDIR /go/src/app
COPY . .

RUN go get .
RUN go build -buildvcs=false . 

FROM gcr.io/distroless/base-debian11

WORKDIR /

COPY --from=builder /go/src/app/ /

CMD ["/goapprtc"]
