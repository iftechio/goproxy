# GOPROXY

[![CircleCI](https://circleci.com/gh/goproxyio/goproxy.svg?style=svg)](https://circleci.com/gh/goproxyio/goproxy)
[![Go Report Card](https://goreportcard.com/badge/github.com/goproxyio/goproxy)](https://goreportcard.com/report/github.com/goproxyio/goproxy)
[![GoDoc](https://godoc.org/github.com/goproxyio/goproxy?status.svg)](https://godoc.org/github.com/goproxyio/goproxy)

A global proxy for go modules. see: [https://goproxy.io](https://goproxy.io)

## Update

本项目用来构建即刻内部的 goproxy 代理，封装了如下细节：

- 通过 proxy 加速访问 github 私有 repo。
- 使用 goproxy.cn 代理 golang 官方 sum.golang.org 的校验。
- 增加一层 cache。

用户在使用时只需要配置如下环境变量即可：

```shell
# GONOSUMDB 让 go cli 对于私有 repo 不校验 sumdb，必须在go get 前配置该变量
# 必须将 GOPRIVATE 变量置为空（若已经为空不用处理），否则 go cli 将不会走 GOPROXY 拉取 GOPRIVATE 中定义的路径
GOPRIVATE="" GONOSUMDB="github.com/iftechio" GOPROXY=http://goproxy.infra.svc.cluster.local:8081 go get -v github.com/iftechio/jike-sdk/go
```

## Requirements
    It invokes the local go command to answer requests.
    The default cacheDir is GOPATH, you can set it up by yourself according to the situation.

## Build
    git clone https://github.com/goproxyio/goproxy.git
    cd goproxy
    make

## Started


### Proxy mode    
    
    ./bin/goproxy -listen=0.0.0.0:80 -cacheDir=/tmp/test

    If you run `go get -v pkg` in the proxy machine, should set a new GOPATH which is different from the old GOPATH, or mayebe deadlock.
    See the file test/get_test.sh.

### Router mode    

Use the -proxy flag switch to "Router mode", which 
implements route filter to routing private module 
or public module .

```
                                         direct
                      +----------------------------------> private repo
                      |
                 match|pattern
                      |
                  +---+---+           +----------+
go get  +-------> |goproxy| +-------> |goproxy.io| +---> golang.org/x/net
                  +-------+           +----------+
                 router mode           proxy mode
```

In Router mode, use the -exclude flag set pattern , direct to the repo which 
match the module path, pattern are matched to the full path specified, not only 
to the host component.

    ./bin/goproxy -listen=0.0.0.0:80 -cacheDir=/tmp/test -proxy https://goproxy.io -exclude "*.corp.example.com,rsc.io/private"

## Use docker image

    docker run -d -p80:8081 goproxy/goproxy

Use the -v flag to persisting the proxy module data (change ___cacheDir___ to your own dir):

    docker run -d -p80:8081 -v cacheDir:/go goproxy/goproxy

## Docker Compose

    docker-compose up

## Appendix

1. set `export GOPROXY=http://localhost` to enable your goproxy.
2. set `export GOPROXY=direct` to disable it.
