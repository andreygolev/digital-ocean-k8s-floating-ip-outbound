### Introduction
Hello, %username%

As you know, Digital Ocean managed kubernetes service (aka DOKS) doesn't have an option to give you either NAT or static outbound public IP.  
Sometimes you man want it, despite your k8s nodes can go up and down, being replaced, etc.

It's possible to send your outbound traffic through the floating IP if you do some tricks mentioned in this article: https://www.digitalocean.com/community/questions/send-outbound-traffic-over-floating-ip

The following tool is a successful attempt to automate this in DOKS.

### Requirements
* You'll need a separate node pool and your workloads requiring known outbound IP pool should have defined affinity/nodeSelector to the nodes from this pool.
* You'll need unassigned Floating IPs as much as your node amount in a pool

This is how it's achieved. No NAT, unfortunately.

### How it works
This tool deploys as a daemonset on all nodes and runs in privileged mode to be able to update route table in host network namespace.

* The tool checks for a hostname match defined in `matchString` parameter
* If hostname of a node doesn't match `matchString`, the tool will simply freeze in endless loop, reporting hourly that there's no match. 
* If there's a match, it will:
  * Check if there's already assigned floating IP
  * If there's floating IP already assigned, it will loop and check every 5 seconds the state. If you unassign IP from a node, it will reassign a free floating IP from a list.
  * If there's no assigned floating IP, it will:
    * Read IP list from `floatingIPs` parameter.  

      **Note**  
      Because of that, you have to preallocate floating IPs in DO and then put them into `floatingIPs` parameter.  
      This is a protection mechanism to not assign some free Floating IP that you have in DO for other needs, and second reason is that this is a way how we define our outbound IP pool.

    * Check if there are still free unassigned Floating IPs left in DO, comparing it with the `floatingIPs`.
    * If there's nothing free, then it will tell about it in a log, and of course you simply need to allocate more IPs in DO.  
    **Note**  
    There should be as many floating IPs as nodes in the pool that is dedicated for static outbound IPs**
    * **If there's free unassgined floating IP, it will simply assign it to self and update route table accordingly, and traffic will flow out of the node with this Floating IP.**
    
    * Label node with `do-outbound-floating-ip-enabled` and possible values `true` / `false`
    * Label node with `do-outbound-floating-ip` and Floating IP address as a value


