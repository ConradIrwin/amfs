package cfg

import (
	"context"
	"net"
)

type Config struct {
	Listen       string
	UnixListen   string
	MountOptions string
	Mounts       []*Mount
}

type Mount struct {
	Name       string
	Mountpoint string

	Source string
}

type ctxKeyType string

var ctxKey = ctxKeyType("amfs.cfg")

func Load(ctx context.Context) (context.Context, error) {
	return context.WithValue(ctx, ctxKey, &Config{
		Listen:       "localhost:51023",
		UnixListen:   "/tmp/amfs.sock",
		MountOptions: "nosuid,noowners,nodev,noac,locallocks", // ,noowners,hard,retrans=1,timeo=5,retry=0,rsize=32768,wsize=32768,local_lock=all",
		Mounts: []*Mount{{
			Name:       "test",
			Mountpoint: "/Users/conrad/0/amfs/test",
			Source:     "localhost:/test",
		}},
	}), nil
}

func Get(ctx context.Context) *Config {
	return ctx.Value(ctxKey).(*Config)
}

func Mounts(ctx context.Context) []*Mount {
	return Get(ctx).Mounts
}

func Listen(ctx context.Context) string {
	return Get(ctx).Listen
}

func UnixListen(ctx context.Context) string {
	return Get(ctx).UnixListen
}

func MountOptions(ctx context.Context) string {
	cfg := Get(ctx)
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		panic(err)
	}

	return "port=" + port + ",mountport=" + port
}
