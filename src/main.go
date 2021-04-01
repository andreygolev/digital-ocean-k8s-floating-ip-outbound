package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/digitalocean/godo"
	"github.com/golang/glog"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var dryRun bool

func init() {
	flag.BoolVar(&dryRun, "dry", true, "Dry run mode. Enabled by default")
	flag.Parse()
}

func main() {
	if dryRun {
		glog.Info("Running in dry mode")
	}

	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		glog.Exit("POD_NAMESPACE not defined")
	}

	configmapName := os.Getenv("CONFIGMAP_NAME")
	if configmapName == "" {
		glog.Exit("CONFIGMAP_NAME not defined")
	}

	nodeName := os.Getenv("HOSTNAME")
	if nodeName == "" {
		glog.Exit("HOSTNAME not defined. Exiting")
	}

	doToken := os.Getenv("DO_TOKEN")
	if doToken == "" {
		glog.Exit("DO_TOKEN not defined")
	}

	nodeFloatingPattern := os.Getenv("HOSTNAME_FLOATING_IP_MATCH_STRING")
	if nodeFloatingPattern == "" {
		glog.Exit("HOSTNAME_FLOATING_IP_MATCH_STRING")
	} else {
		glog.V(4).Infof("Checking hostname %v over pattern: %v", nodeName, nodeFloatingPattern)
	}

	doClient := godo.NewFromToken(doToken)

	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		glog.Exit(err)
	}

	k8sClient, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		glog.Exit(err)
	}

CHECKLOOP:
	if strings.Contains(nodeName, nodeFloatingPattern) == false {
		glog.V(4).Infof("This node %v doesn't match pattern: %v. Skipping", nodeName, nodeFloatingPattern)
		time.Sleep(1 * time.Hour)
		goto CHECKLOOP // loop forever
	}

	for {
		glog.V(4).Info("Checking if node has already assigned floating IP")
		resp, err := http.Get("http://169.254.169.254/metadata/v1/floating_ip/ipv4/active")
		if err != nil {
			glog.Errorf("Error checking floating IP status: %v", err)
		}
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		isActiveFloatingIP := buf.String()
		resp.Body.Close()

		if isActiveFloatingIP == "false" {
			glog.Info("Node doesn't have floating IP assigned, but should. Starting assignment procedure...")
			break
		}

		if isActiveFloatingIP == "true" {
			glog.Info("Floating IP already assigned. Sleeping for 5 seconds")
		}

		time.Sleep(5 * time.Second)
	}

	cm, err := k8sClient.CoreV1().ConfigMaps(podNamespace).Get(context.TODO(), configmapName, v1.GetOptions{})
	if err != nil {
		glog.Exit(err)
	}

	cmStripped := strings.ReplaceAll(cm.Data["ips"], " ", "")
	cmIPS := strings.Split(cmStripped, ",")
	glog.V(3).Infof("Floating IPs from configmap: %+v", cmIPS)

	resp, err := http.Get("http://169.254.169.254/metadata/v1/id")
	if err != nil {
		glog.Errorf("Error fetching Droplet ID: %v", err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	dropletID, err := strconv.Atoi(buf.String())
	if err != nil {
		glog.Errorf("Error converting Droplet ID to int")
	}
	resp.Body.Close()
	glog.V(3).Infof("Got Droplet ID: %v", dropletID)

	d := godo.Droplet{ID: dropletID}

	floatingIPS, _, err := doClient.FloatingIPs.List(context.TODO(), &godo.ListOptions{})
	if err != nil {
		glog.Infof("Error getting floating IPs from DO: %v", err)
	}

	var unassignedFloatingIPs []string
	for _, x := range floatingIPS {
		glog.V(3).Infof("Found floating ip in DO: %+v", x.IP)
		if x.Droplet == nil {
			unassignedFloatingIPs = append(unassignedFloatingIPs, x.IP)
		}
	}

	// Check if there's an unassigned IP matching list from configmap
	for _, x := range cmIPS {
		glog.V(4).Infof("Checking %v over %v", x, unassignedFloatingIPs)
		if contains(unassignedFloatingIPs, x) {
			err := assignIP(x, doClient, &d)
			if err == nil {
				mainLabelPatch := fmt.Sprintf(`[{"op":"add","path":"/metadata/labels/%s","value":"%s" }]`, "do-outbound-floating-ip-enabled", "true")
				glog.V(3).Infof("Labeling node: %v", mainLabelPatch)
				ipLabelPatch := fmt.Sprintf(`[{"op":"add","path":"/metadata/labels/%s","value":"%s" }]`, "do-outbound-floating-ip", x)
				glog.V(3).Infof("Labeling node: %v", ipLabelPatch)
				res, err := k8sClient.CoreV1().Nodes().Patch(context.TODO(), nodeName, types.JSONPatchType, []byte(mainLabelPatch), v1.PatchOptions{})
				if err != nil {
					glog.Errorf("Error labeling node: %v", err)
				}
				glog.V(3).Infof("Labeled node: %v", res.Name)

				res, err = k8sClient.CoreV1().Nodes().Patch(context.TODO(), nodeName, types.JSONPatchType, []byte(ipLabelPatch), v1.PatchOptions{})
				if err != nil {
					glog.Errorf("Error labeling node: %v", err)
				}
				glog.V(3).Infof("Labeled node: %v", res.Name)

				break // Not trying with another IP if assignment was successful
			}
		}
	}

	glog.V(4).Info("Sleeping for 1 minute after the hard job")
	time.Sleep(1 * time.Minute)
	goto CHECKLOOP
}

func assignIP(x string, c *godo.Client, d *godo.Droplet) error {
	glog.V(3).Infof("Assigning IP: %v to Droplet %v", x, d.ID)
	if dryRun == false {
		_, _, err := c.FloatingIPActions.Assign(context.TODO(), x, d.ID)
		if err != nil {
			return err
		}
		glog.V(3).Infof("Successfully assigned IP: %v to Droplet %v", x, d.ID)
	}

	glog.V(3).Info("Updating route table")
	err := updateRouteTable()
	if err != nil {
		return err
	}
	glog.V(3).Infof("Succesfully assigned: %v", x)
	return nil
}

func updateRouteTable() error {
	resp, err := http.Get("http://169.254.169.254/metadata/v1/interfaces/public/0/anchor_ipv4/gateway")
	if err != nil {
		return err
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	anchorGW := buf.String()
	resp.Body.Close()
	glog.V(3).Infof("Got anchor GW: %v", anchorGW)

	resp, err = http.Get("http://169.254.169.254/metadata/v1/interfaces/public/0/ipv4/gateway")
	if err != nil {
		return err
	}
	buf = new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	oldGW := buf.String()
	resp.Body.Close()
	glog.V(3).Infof("Got old GW: %v", oldGW)

	if dryRun == false {
		glog.V(3).Infof("Adding new default GW: %v", anchorGW)
		cmd := exec.Command("/sbin/route", "add", "default", "gw", anchorGW)
		err = cmd.Run()

		if err != nil {
			glog.Error("Error adding new default gw: %v\t", err, anchorGW)
			return err
		}
	}

	if dryRun == false {
		glog.V(3).Infof("Deleting old default GW: %v", oldGW)
		cmd := exec.Command("/sbin/route", "del", "default", "gw", oldGW)
		err = cmd.Run()

		if err != nil {
			glog.Error("Error deleting old default gw: %v\t%v", err, oldGW)
			return err
		}
	}

	return nil
}

func contains(slice []string, item string) bool {
	set := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		set[s] = struct{}{}
	}

	_, ok := set[item]
	if ok {
		glog.V(4).Info("It's a match")
	}
	return ok
}
