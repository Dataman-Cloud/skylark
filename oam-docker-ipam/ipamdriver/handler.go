package ipamdriver

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/libkv/store"
	netlabel "github.com/docker/libnetwork/netlabel"

	ipam "oam-docker-ipam/skylarkcni/ipamapi"
	"oam-docker-ipam/util"
)

type MyIPAMHandler struct {
}

func (iph *MyIPAMHandler) GetCapabilities() (response *ipam.CapabilitiesResponse, err error) {
	log.Infof("GetCapabilities")
	return &ipam.CapabilitiesResponse{RequiresMACAddress: true}, nil
}

func (iph *MyIPAMHandler) GetDefaultAddressSpaces() (response *ipam.AddressSpacesResponse, err error) {
	log.Infof("GetDefaultAddressSpaces")
	return &ipam.AddressSpacesResponse{}, nil
}

func (iph *MyIPAMHandler) RequestPool(request *ipam.RequestPoolRequest) (response *ipam.RequestPoolResponse, err error) {
	var request_json []byte = nil
	request_json, err = json.Marshal(request)
	if err != nil {
		return nil, err
	}
	log.Infof("RequestPool: %s", request_json)
	ip_net, _ := util.GetIPNetAndMask(request.Pool)
	_, ip_cidr := util.GetIPAndCIDR(request.Pool)
	options := request.Options
	return &ipam.RequestPoolResponse{ip_net, ip_cidr, options}, nil
}

func (iph *MyIPAMHandler) ReleasePool(request *ipam.ReleasePoolRequest) (err error) {
	var request_json []byte = nil
	request_json, err = json.Marshal(request)
	if err != nil {
		return err
	}
	log.Infof("ReleasePool %s is danger, you should do this by manual.", request_json)
	return nil
}

func (iph *MyIPAMHandler) RequestAddress(request *ipam.RequestAddressRequest) (*ipam.RequestAddressResponse, error) {
	var (
		reqID      = util.RandomString(16)
		ip_net     = request.PoolID  // subnet id
		preferAddr = request.Address // prefered IP, container with fixed ip: `--ip`
		config, _  = GetConfig(ip_net)
	)

	bs, _ := json.Marshal(request)
	log.Printf("IPAM RequestAddress %s payload: %s", reqID, string(bs))

	if value, ok := request.Options["RequestAddressType"]; ok && value == netlabel.Gateway {
		log.Printf("IPAM RequestAddress %s allocate gateway address %s", reqID, preferAddr)
		return &ipam.RequestAddressResponse{fmt.Sprintf("%s/%s", preferAddr, config.Mask), nil}, nil
	}

	// request on docker container start up
	var (
		respAddr string
		err      error
	)
	for {
		respAddr, err = AllocateIP(ip_net, preferAddr)
		if err == nil {
			break
		}
		// random assigned ip is occupied by other racer, retry ..
		if err == store.ErrKeyModified {
			log.Warnf("IPAM RequestAddress %s met a racer, retry another random assignment ...", reqID)
			time.Sleep(time.Millisecond * 500)
			continue
		}
		log.Errorf("IPAM RequestAddress %s error: %v", reqID, err)
		return nil, err
	}

	log.Printf("IPAM RequestAddress %s Allocated IP Address: %s", reqID, respAddr)
	return &ipam.RequestAddressResponse{fmt.Sprintf("%s/%s", respAddr, config.Mask), nil}, nil
}

func (iph *MyIPAMHandler) ReleaseAddress(request *ipam.ReleaseAddressRequest) error {
	var (
		reqID  = util.RandomString(16)
		ip_net = request.PoolID  // subnet id
		ipAddr = request.Address // target ip
	)

	bs, _ := json.Marshal(request)
	log.Printf("IPAM ReleaseAddress %s payload: %s", reqID, string(bs))

	if err := ReleaseIP(ip_net, ipAddr); err != nil {
		log.Errorf("IPAM ReleaseAddress %s error: %v", reqID, err)
		return err
	}

	log.Printf("IPAM ReleaseAddress %s Released IP Address: %s", reqID, ipAddr)
	return nil
}

func (iph *MyIPAMHandler) GetAddress(request *ipam.GetAddressRequest) (*ipam.GetAddressResponse, error) {
	return nil, errors.New("this implemention temporarily closed")
}
