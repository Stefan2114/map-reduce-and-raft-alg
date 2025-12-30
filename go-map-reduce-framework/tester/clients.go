package tester

import (
	//"log"
	"go-map-reduce-framework/srv/labrpc"
	"sync"
)

type end struct {
	name string
	end  *labrpc.ClientEnd
}

// Servers are named by ServerName() and clerks lazily make a
// per-clerk ClientEnd to a server.  Each clerk has a Client with a map
// of the allocated ends for this clerk.
type Client struct {
	mu   sync.Mutex
	net  *labrpc.Network
	ends map[string]end

	// if nil client can connect to all servers
	// if len(servers) = 0, client cannot connect to any servers
	servers []string
}

func makeClientTo(net *labrpc.Network, srvs []string) *Client {
	return &Client{ends: make(map[string]end), net: net, servers: srvs}
}

// caller must acquire lock
func (client *Client) allowedL(server string) bool {
	if client.servers == nil {
		return true
	}
	for _, n := range client.servers {
		if n == server {
			return true
		}
	}
	return false
}

func (client *Client) makeEnd(server string) end {
	client.mu.Lock()
	defer client.mu.Unlock()

	if end, ok := client.ends[server]; ok {
		return end
	}

	name := Randstring(20)
	//log.Printf("%p: makEnd %v %v allowed %t", client, name, server, client.allowedL(server))
	end := end{name: name, end: client.net.MakeEnd(name)}
	client.net.Connect(name, server)
	if client.allowedL(server) {
		client.net.Enable(name, true)
	} else {
		client.net.Enable(name, false)
	}
	client.ends[server] = end
	return end
}

func (client *Client) Call(server, method string, args interface{}, reply interface{}) bool {
	end := client.makeEnd(server)
	ok := end.end.Call(method, args, reply)
	// log.Printf("%p: Call done e %v m %v %v %v ok %v", client, end.name, method, args, reply, ok)
	return ok
}

func (client *Client) ConnectAll() {
	client.mu.Lock()
	defer client.mu.Unlock()

	for _, e := range client.ends {
		//log.Printf("%p: ConnectAll: enable %v", client, e.name)
		client.net.Enable(e.name, true)
	}
	client.servers = nil
}

func (client *Client) ConnectTo(srvs []string) {
	client.mu.Lock()
	defer client.mu.Unlock()

	// log.Printf("%p: ConnectTo: enable %v", client, servers)
	client.servers = srvs
	for srv, e := range client.ends {
		if client.allowedL(srv) {
			client.net.Enable(e.name, true)
		}
	}
}

func (client *Client) Disconnect(srv string) {
	client.mu.Lock()
	defer client.mu.Unlock()

	for s, e := range client.ends {
		if s == srv {
			//log.Printf("%p: Disconnect: disable %v %s", client, srv)
			client.net.Enable(e.name, false)
		}
	}
}

func (client *Client) DisconnectAll() {
	client.mu.Lock()
	defer client.mu.Unlock()

	for _, e := range client.ends {
		//log.Printf("%p: DisconnectAll: disable %v", client, e.name)
		client.net.Enable(e.name, false)
	}
	client.servers = make([]string, 0)
}

func (client *Client) remove() {
	client.mu.Lock()
	defer client.mu.Unlock()

	for _, e := range client.ends {
		client.net.DeleteEnd(e.name)
	}
}

type Clients struct {
	mu     sync.Mutex
	net    *labrpc.Network
	clerks map[*Client]struct{}
}

func makeClients(net *labrpc.Network) *Clients {
	clients := &Clients{net: net, clerks: make(map[*Client]struct{})}
	return clients
}

func (clients *Clients) makeEnd(servername string) *labrpc.ClientEnd {
	name := Randstring(20)
	end := clients.net.MakeEnd(name)
	clients.net.Connect(name, servername)
	clients.net.Enable(name, true)
	return end
}

// Create a clnt for a clerk with specific server names, but allow
// only connections to connections to servers in to[].
func (clients *Clients) MakeClient() *Client {
	return clients.MakeClientTo(nil)
}

func (clients *Clients) MakeClientTo(servers []string) *Client {
	clients.mu.Lock()
	defer clients.mu.Unlock()
	client := makeClientTo(clients.net, servers)

	clients.clerks[client] = struct{}{}
	return client
}

func (clients *Clients) cleanup() {
	clients.mu.Lock()
	defer clients.mu.Unlock()

	for client, _ := range clients.clerks {
		client.remove()
	}
	clients.clerks = nil
}

func (clients *Clients) DeleteClient(client *Client) {
	clients.mu.Lock()
	defer clients.mu.Unlock()

	if _, ok := clients.clerks[client]; ok {
		client.remove()
		delete(clients.clerks, client)
	}
}
