package ipamdriver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	//"github.com/docker/go-plugins-helpers/ipam"
	ipam "oam-docker-ipam/skylarkcni/ipamapi"

	"github.com/docker/engine-api/client"
	"github.com/docker/engine-api/types"
	"github.com/docker/libkv/store"
	"golang.org/x/net/context"

	"oam-docker-ipam/db"
	_ "oam-docker-ipam/debug"
	"oam-docker-ipam/util"
)

var (
	hostname string
)

func init() {
	var err error
	hostname, err = os.Hostname()
	if err != nil {
		log.Fatalf("Could not retrieve hostname: %v", err)
	}
}

type Config struct {
	Ipnet string
	Mask  string
}

func StartServer() {
	log.Infof("Server start with hostname: %s", hostname)

	// Release the ip resources that occupied by dead containers in localhost
	IpResourceCleanUP()

	//Create etcd watcher and event handler for rate limit change
	// evch, err := db.WatchDir(db.KeyNetwork, make(chan struct{}))
	// if err != nil {
	// log.Fatalln(err)
	// }
	// go handleChannelEvent(evch)

	d := &MyIPAMHandler{}
	h := ipam.NewHandler(d)
	err := h.ServeUnix("root", "skylark")
	if err != nil {
		log.Fatalln("IPAM Serve error", err)
	}
}

func AllocateIPRange(ip_start, ip_end string) []string {
	ips := util.GetIPRange(ip_start, ip_end)
	ip_net, mask := util.GetIPNetAndMask(ip_start)
	for _, ip := range ips {
		if assigned, _ := checkIPAssigned(ip_net, ip); assigned {
			log.Warnf("IP %s has been allocated", ip)
			continue
		}
		db.SetKey(db.Normalize(db.KeyNetwork, ip_net, "pool", ip), "")
	}
	initializeConfig(ip_net, mask)
	fmt.Println("Allocate Containers IP Done! Total:", len(ips))
	return ips
}

func ReleaseIP(ip_net, ip string) error {
	// remove from local assigneds
	err := db.DeleteKey(db.Normalize(db.KeyNetwork, ip_net, "assigned", hostname, ip))
	if err != nil {
		if err == store.ErrKeyNotFound {
			log.Infof("Skip Release UnAssigned IP %s", ip)
			return nil
		}
		log.Errorf("Release IP %s error: %v", ip, err)
		return err
	}

	// put back to pool
	err = db.SetKey(db.Normalize(db.KeyNetwork, ip_net, "pool", ip), "")
	if err != nil {
		// TODO roll back required
		return err
	}

	return nil
}

func AllocateIP(ip_net, ip string) (string, error) {
	lock, _ := db.NewLock(db.Normalize(db.KeyNetwork, ip_net, "wait"), nil) // etcd implements never return a non-nil error

	var err error
	for i := 1; i <= 10; i++ {
		stopCh := make(chan struct{})
		timer := time.AfterFunc(time.Second*1, func() {
			close(stopCh)
		})
		_, err = lock.Lock(stopCh)
		if err != nil {
			log.Warnf("db store Lock() error: %v, retry ...", err)
		} else {
			timer.Stop()
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("db store Lock() error: %v", err)
	}

	defer lock.Unlock()
	log.Debugf("got db lock to take ip address: %s:%s", ip_net, ip)

	ip, err = getIP(ip_net, ip)
	if err != nil {
		return "", fmt.Errorf("db store getIP() error: %v", err)
	}

	log.Infof("Allocated IP %s", ip)
	return ip, nil
}

func getIP(ip_net, ip string) (string, error) {
	ip_pool, err := db.ListKeyNames(db.Normalize(db.KeyNetwork, ip_net, "pool"))
	if err != nil {
		return "", fmt.Errorf("fetch ip pool error: %v", err)
	}
	if len(ip_pool) == 0 {
		return "", errors.New("empty ip pool")
	}

	// no prefered ip given, pick up first ip in the pool
	if ip == "" {
		ip = ip_pool[0]
		log.Debugf("no prefered ip given, pick up the first ip [%s] in the pool ...", ip)
	}

	// ensure ip not empty
	if ip == "" {
		return "", fmt.Errorf("empty ip address")
	}

	assigned, err := checkIPAssigned(ip_net, ip)
	if err != nil {
		return "", fmt.Errorf("check ip %s assigned error: %v", ip, err)
	}
	if assigned {
		return "", fmt.Errorf("ip %s has been allocated", ip)
	}

	// move ip from pool to assigned
	err = db.DeleteKey(db.Normalize(db.KeyNetwork, ip_net, "pool", ip))
	if err != nil {
		return "", fmt.Errorf("remove ip %s from pool error: %v", ip, err)
	}
	err = db.SetKey(db.Normalize(db.KeyNetwork, ip_net, "assigned", hostname, ip), "")
	if err != nil {
		// TODO roll back required
		return "", fmt.Errorf("put ip %s to assigned error: %v", ip, err)
	}
	// query container env, save flow limit setting into the kv store if available
	// go updateFlowLimit(ip_net, ip)
	return ip, nil
}

func checkIPAssigned(ip_net, ip string) (bool, error) {
	base := db.Normalize(db.KeyNetwork, ip_net, "assigned")
	hosts, err := db.ListKeyNames(base)
	if err != nil {
		if err == store.ErrKeyNotFound {
			return false, nil
		}
		return false, err
	}
	for _, host := range hosts {
		if exist := db.IsKeyExist(db.Normalize(base, host, ip)); exist {
			return true, nil
		}
	}
	return false, nil
}

func initializeConfig(ip_net, mask string) error {
	config := &Config{Ipnet: ip_net, Mask: mask}
	config_bytes, err := json.Marshal(config)
	if err != nil {
		log.Fatal(err)
	}
	err = db.SetKey(db.Normalize(db.KeyNetwork, ip_net, "config"), string(config_bytes))
	if err == nil {
		log.Infof("Initialized Config %s for network %s", string(config_bytes), ip_net)
	}
	return err
}

func DeleteNetWork(ip_net string) error {
	err := db.DeleteKey(db.Normalize(db.KeyNetwork, ip_net))
	if err == nil {
		log.Infof("DeleteNetwork %s", ip_net)
	}
	return err
}

func GetConfig(ip_net string) (*Config, error) {
	config, err := db.GetKey(db.Normalize(db.KeyNetwork, ip_net, "config"))
	conf := &Config{}
	json.Unmarshal([]byte(config), conf)
	return conf, err
}

func IpResourceCleanUP() {
	// get all the ip subnet name to the slice
	log.Info("start to clean up ip resource in current host ...")
	subnets, _ := db.ListKeyNames(db.KeyNetwork)
	log.Info("subnet info:", subnets)

	var host_assinged_ips []string
	for _, subnet := range subnets {
		key_assigned := db.Normalize(db.KeyNetwork, subnet, "assigned", hostname)
		if db.IsKeyExist(key_assigned) {
			// get all the assigned ips in current host
			assigned_ips, _ := db.ListKeyNames(key_assigned)
			host_assinged_ips = append(host_assinged_ips, assigned_ips...)
			log.Info("subnet ip keys:", assigned_ips)
		}
	}
	log.Info("assigeded ips in this host:", host_assinged_ips)

	// get all the active ips in this host
	var active_ips []string
	containers, _ := ListContainers("unix:///var/run/docker.sock")
	for _, container := range containers {
		// get network setting in each container
		networks := container.NetworkSettings.Networks
		for _, n := range networks {
			log.Info("Found active IP:", n.IPAddress)
			active_ips = append(active_ips, n.IPAddress)
		}
	}
	log.Info("active IPs:", active_ips)

	// find residual ips and clean up
	var found bool = false
	for _, ip := range host_assinged_ips {
		for _, active_ip := range active_ips {
			if ip == active_ip {
				// this is active
				found = true
			}
		}
		//if no active ip found, clean up this ip
		if found == false {
			for _, subnet := range subnets {
				key_to_delete := db.Normalize(db.KeyNetwork, subnet, "assigned", hostname, ip)
				if db.IsKeyExist(key_to_delete) {
					ReleaseIP(subnet, ip)
				}
			}
		}
		found = false
	}

}

func ListContainers(socketurl string) ([]types.Container, error) {
	var c *client.Client
	var err error
	defaultHeaders := map[string]string{"User-Agent": "engine-api-cli-1.0"}
	c, err = client.NewClient(socketurl, "", nil, defaultHeaders)
	if err != nil {
		log.Errorf("Create Docker Client error", err)
		return nil, err
	}

	// List containers
	opts := types.ContainerListOptions{}
	ctx, cancel := context.WithTimeout(context.Background(), 20000*time.Millisecond)
	defer cancel()
	containers, err := c.ContainerList(ctx, opts)
	if err != nil {
		log.Errorf("List Container error", err)
		return nil, err
	}
	return containers, err
}

func InspectContainer(socketurl string, id string) (types.ContainerJSON, error) {
	var c *client.Client
	var err error
	defaultHeaders := map[string]string{"User-Agent": "engine-api-cli-1.0"}
	c, err = client.NewClient(socketurl, "", nil, defaultHeaders)
	if err != nil {
		log.Errorln("Create Docker Client error", err)
		return types.ContainerJSON{}, err
	}

	// Inspect container
	ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
	defer cancel()
	containerJson, err := c.ContainerInspect(ctx, id)
	if err != nil {
		log.Errorf("Inspect Container error: %s", id)
		return types.ContainerJSON{}, err
	}
	return containerJson, err

}

func updateFlowLimit(ip_net string, ip string) {
	// wait for container creation finished otherwise it will be blocked
	time.Sleep(time.Second)
	target_ip := ip
	var envstr string
	var tmp string
	var limitstr string
	containers, _ := ListContainers("unix:///var/run/docker.sock")
	for _, container := range containers {
		// get network setting in each container
		networks := container.NetworkSettings.Networks
		for _, v := range networks {
			if v.IPAddress == target_ip {
				containerJson, _ := InspectContainer("unix:///var/run/docker.sock", container.ID)

				//get pid and env of target container
				pid := containerJson.State.Pid
				env := containerJson.Config.Env
				//get the env string and put into a map of env
				if len(env) != 0 {
					//get limit setting from env and set flow control
					for _, s := range env {
						s1 := strings.ToUpper(s)
						if strings.HasPrefix(s1, "IN=") || strings.HasPrefix(s1, "OUT=") {
							s2 := strings.Split(s1, "=")
							tmp = tmp + "\"" + s2[0] + "\"" + ":" + s2[1] + ","
						}
					}

					//if no limit setting in env set as no flow control
					if tmp == "" {
						limitstr = "\"IN\":0,\"OUT\":0"
					} else {
						limitstr = strings.TrimRight(tmp, ",")
					}
					envstr = strings.Join([]string{"{", limitstr, "}"}, "")
					log.Debug("Pid: ", pid)
					log.Debug("Env: ", env)
					log.Debug("envstr: ", envstr)
				} else {
					//if no env set default to no flow control
					envstr = "{\"IN\":0,\"OUT\":0}"
					//set flow control to zero
					log.Debug("no env found for container: ", container.ID)
				}
				db.SetKey(db.Normalize(db.KeyNetwork, ip_net, "assigned", hostname, ip), envstr)
			}
		}
	}
}

func handleChannelEvent(evch <-chan *store.KVPair) {
	var limit map[string]int

	for {

		kvPair := <-evch

		key := kvPair.Key

		// skip event not for current host ...
		if !strings.Contains(key, hostname) {
			continue
		}

		//get the ip addr in key like /skylark/containers/10.0.2.0/assigned/skylark-1/10.0.2.103
		target_ip := filepath.Base(key)
		if net.ParseIP(target_ip) == nil {
			log.Debugf("skip invalid ip addr: %s", target_ip)
			continue
		}

		//convert the flow limit from string to map
		json.Unmarshal(kvPair.Value, &limit)

		//if both IN and OUT are 0, then no limit is set
		if limit["IN"] == 0 || limit["OUT"] == 0 {
			log.Debug("skip unlimit flow settings")
			continue
		}

		//get corresponding container who owns the target_ip
		containers, _ := ListContainers("unix:///var/run/docker.sock")
		for _, container := range containers {
			// get network setting in each container
			networks := container.NetworkSettings.Networks
			for _, v := range networks {
				if v.IPAddress == target_ip {
					containerJson, _ := InspectContainer("unix:///var/run/docker.sock", container.ID)

					//get pid of target container
					pid := containerJson.State.Pid

					//set flow limit
					go set_flow_limit(pid, limit)
					log.Debug(pid, ":", limit)
				}
			}
		}
	}
}

func set_flow_limit(pid int, limit map[string]int) {
	in, foundin := limit["IN"]
	out, foundout := limit["OUT"]
	var dev_substr string
	if foundin && foundout {
		host_veth_num := get_host_veth_num(pid, "eth0")

		//search for corresponding veth device in host
		cmd := exec.Command("ip", "link")
		log.Debug(cmd)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Error(output)
			return
		}
		iplinks := string(output)

		//find the match for like 5: veth25c84ba@if4: <BROADCAST ...
		linkslice := strings.Split(iplinks, "\n")
		reg := regexp.MustCompile("^" + host_veth_num + ":")
		for _, s := range linkslice {
			if reg.MatchString(s) {
				dev_substr = s
			}
		}

		trim_left := strings.TrimLeft(dev_substr, host_veth_num+": ")
		trim_right_index := strings.Index(trim_left, "@")
		trim_right := trim_left[:trim_right_index]
		host_veth_dev := trim_right
		log.Debug("Target host device: ", host_veth_dev)

		//set the flow limit with wondershaper
		cmd = exec.Command("/sbin/wondershaper", host_veth_dev,
			strconv.Itoa(out), strconv.Itoa(in))
		log.Debug(cmd)
		output, err = cmd.CombinedOutput()
		if err != nil {
			log.Error(output)
			return
		}

		//show the flow limit result
		cmd = exec.Command("/sbin/tc", "qdisc", "show")
		log.Debug(cmd)
		output, err = cmd.CombinedOutput()
		if err != nil {
			log.Error(output)
			return
		}
		log.Debug(string(output))
		log.Debug("Flow limit setting successful!")
		log.Debug("Pid: ", pid)
		log.Debug("Limit: ", limit)
	} else {
		log.Error("Flow limit parameter incomplete!")
	}

}

//this function relay on nsenter tool
func get_host_veth_num(pid int, device string) string {
	//invoke nsenter
	cmd := "nsenter"
	args := fmt.Sprint("-t ",
		strconv.Itoa(pid)+" ",
		"-n ",
		"ip link show "+device)
	log.Debug(cmd)
	output, err := exec.Command(cmd, strings.Split(args, " ")...).CombinedOutput()
	if err != nil {
		log.Error(output)
		return ""
	}
	//output sth like "37256: eth0@if37257: <BROADCAST,MULTICAST,UP,LOWER_UP>"

	//get substring about ethif
	ethif_slice := strings.Split(string(output), " ")[1]
	ethif := strings.TrimRight(ethif_slice, ":")
	ifnum := strings.TrimLeft(ethif, device+"@if")
	log.Debug("Got veth number: ", ifnum)
	return ifnum
}
