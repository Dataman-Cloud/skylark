package db

import (
	"path"
	"path/filepath"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/docker/libkv"
	"github.com/docker/libkv/store"
	etcdkv "github.com/docker/libkv/store/etcd"
)

var (
	kv store.Store
)

const (
	KeyNetwork = "/skylark/networks"
	KeyPod     = "/skylark/pods"
)

func init() {
	etcdkv.Register()
}

func Setup(addr string) {
	var (
		etcdAddrs []string
		err       error
	)
	for _, addr := range strings.Split(addr, ",") {
		cleanAddr := strings.TrimSpace(strings.TrimPrefix(addr, "http://"))
		if cleanAddr != "" {
			etcdAddrs = append(etcdAddrs, cleanAddr)
		}
	}
	if len(etcdAddrs) == 0 {
		log.Fatalln("no avaliable etcd cluster addresses given")
	}

	kv, err = libkv.NewStore(
		store.ETCD,
		etcdAddrs,
		&store.Config{
			ConnectionTimeout: time.Second * 10,
		})
	if err != nil {
		log.Fatalln(err)
	}

	for _, key := range []string{KeyNetwork, KeyPod} {
		if ok, _ := kv.Exists(key); ok {
			continue
		}
		kv.Put(key, nil, &store.WriteOptions{IsDir: true})
	}
}

func Normalize(keys ...string) string {
	return path.Clean(path.Join(keys...))
}

func GetKey(key string) (string, error) {
	kvPair, err := kv.Get(key)
	if err != nil {
		return "", err
	}
	return string(kvPair.Value), nil
}

// list sub keys base name of given path
func ListKeyNames(dir string) ([]string, error) {
	kvPairs, err := kv.List(dir)
	if err != nil {
		return nil, err
	}

	ret := make([]string, 0, 0)
	for _, kvPair := range kvPairs {
		ret = append(ret, filepath.Base(kvPair.Key))
	}
	return ret, nil
}

func IsKeyExist(key string) bool {
	ok, _ := kv.Exists(key)
	return ok
}

func SetKey(key, value string) error {
	return kv.Put(key, []byte(value), nil)
}

func DeleteKey(key string) error {
	return kv.DeleteTree(key)
}

func WatchDir(dir string, stop chan struct{}) (<-chan *store.KVPair, error) {
	return kv.WatchTree(dir, stop)
}

func NewLock(key string, options *store.LockOptions) (store.Locker, error) {
	return kv.NewLock(key, options)
}
