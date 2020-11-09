package senpai

import (
	"io/ioutil"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Addr     string
	Nick     string
	Real     string
	User     string
	Password *string

	Highlights    []string
	OnHighlight   string   `yaml:"on-highlight"`
	NickColWidth  int      `yaml:"nick-column-width"`
	ChanColWidth  int      `yaml:"chan-column-width"`
	NickListWidth int      `yaml:"nick-list-width"`

	Debug bool
}

func ParseConfig(buf []byte) (cfg Config, err error) {
	err = yaml.Unmarshal(buf, &cfg)
	if cfg.NickColWidth <= 0 {
		cfg.NickColWidth = 16
	}
	if cfg.ChanColWidth <= 0 {
		cfg.ChanColWidth = 16
	}
	if cfg.NickListWidth <= 0 {
		cfg.NickListWidth = 16
	}
	return
}

func LoadConfigFile(filename string) (cfg Config, err error) {
	var buf []byte

	buf, err = ioutil.ReadFile(filename)
	if err != nil {
		return
	}

	cfg, err = ParseConfig(buf)

	return
}
