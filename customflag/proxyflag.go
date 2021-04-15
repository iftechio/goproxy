package customflag

import (
	"fmt"
	"net/url"
)

type ProxyFlag struct {
	URLs []*url.URL
}

func (p *ProxyFlag) String() string {
	return fmt.Sprint(p.URLs)
}

func (p *ProxyFlag) Set(value string) error {
	if parsedUrl, err := url.Parse(value); err != nil {
		return err
	} else {
		p.URLs = append(p.URLs, parsedUrl)
	}
	return nil
}
