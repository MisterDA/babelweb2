package ws

import (
	"babelweb2/parser"
	"bufio"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

const (
	delete = iota
	update = iota
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

var Db dataBase

type Message struct {
	action  string
	message string
}

//Message messages to send to the client via websocket
// type Message map[string]interface{}

type dataBase struct {
	sync.Mutex
	Bd parser.BabelDesc
}

//MCUpdates multicast updates sent by the routine comminicating with the routers
func MCUpdates(updates chan parser.BabelUpdate, g *Listenergroupe,
	wg *sync.WaitGroup) {
	wg.Add(1)
	for {
		update, quit := <-updates
		if !quit {
			log.Println("closing all channels")
			g.Iter(func(l *Listener) {
				close(l.conduct)
			})
			wg.Done()
			return
		}
		if !(Db.Bd.CheckUpdate(update)) {
			// log.Println("not sending : ", update)
			continue
		}
		// log.Println("sending : ", update)
		Db.Lock()
		err := Db.Bd.Update(update)
		if err != nil {
			log.Println(err)
		}
		Db.Unlock()
		t := update.ToS()
		g.Iter(func(l *Listener) {
			l.conduct <- t
		})
		//TODO unlock()
	}
}

//GetRouterMess gets messages sent by the current router and redirect them to
//the rMess channel
func GetRouterMess(s *bufio.Scanner, rMess chan string, quit chan struct{}) {
	for {
		select {
		case <-quit:
			return
		default:
			s.Scan()
			if len(s.Text()) != 0 {
				log.Println(s.Text())
				rMess <- s.Text()
			}
		}
	}
}

//GetMess gets messages sent by the client and redirect them to the mess chanel
func GetMess(conn *websocket.Conn, mess chan []byte) {
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println(err)
			close(mess)
			return
		}
		mess <- message
	}
}

//HandleMessage handle messages receved from the client
func HandleMessage(mess []byte, conn *websocket.Conn, telnetcon *net.Conn, quit chan struct{}, rMess chan string) {
	var m2c Message
	var err error
	temp := strings.Split(string(mess), " ")
	log.Println(temp)

	if temp[0] == "connect" {
		//the ip we're asked to connect is valide
		if net.ParseIP(temp[1]) != nil {
			node := "[" + temp[1] + "]:33123"
			//it's the first connection
			if (*telnetcon) != nil {
				log.Println("closing previous connection")
				quit <- struct{}{}
				(*telnetcon).Close()
			}
			*telnetcon, err = net.Dial("tcp6", node)
			s := bufio.NewScanner(bufio.NewReader(*telnetcon))
			go GetRouterMess(s, rMess, quit)
			if err != nil {
				log.Println("connection error")
				m2c.message = "error"
				error := conn.WriteJSON(m2c)
				if error != nil {
					log.Println(err)
				}
			} else { //connection successfull
				log.Println("connected")
				m2c.message = "connected"
				error := conn.WriteJSON(m2c)
				if error != nil {
					log.Println(err)
				}
			}
		} else {
			m2c.message = "not an ip"
			error := conn.WriteJSON(m2c)
			if error != nil {
				log.Println("not an ip")
			}
		}
	} else {
		if (*telnetcon) == nil {
			log.Println("not connected")
		} else {
			log.Println("sending: ", string(mess))
			_, error := (*telnetcon).Write(append(mess, byte('\n')))
			if error != nil {
				log.Println(error)
			}
		}
	}
	conn.WriteJSON(m2c)
	return
}

//Handler manage the websockets
func Handler(l *Listenergroupe) http.Handler {
	var m2c Message
	m2c.action = "client"

	quit := make(chan struct{})
	fn := func(w http.ResponseWriter, r *http.Request) {

		log.Println("New connection to a websocket")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Could not create the socket.", err)
			return
		}
		log.Println("    Sending the database to the new client")
		Db.Lock()
		Db.Bd.Iter(func(bu parser.BabelUpdate) error {
			sbu := bu.ToS()
			err := conn.WriteJSON(sbu)
			if err != nil {
				log.Println(err)
			}
			return err
		})

		updates := NewListener()
		l.Push(updates)
		Db.Unlock()
		defer l.Flush(updates)

		var telnetconn net.Conn

		messFromClient := make(chan []byte, ChanelSize)
		messFromRouter := make(chan string, 2)
		go GetMess(conn, messFromClient)

		for {
			//we wait for a new message from the client or from our channel
			select {
			case lastUp := <-updates.conduct: //we got a new update on the channel

				// log.Println("sending:\n", lastUp)

				err := conn.WriteJSON(lastUp)
				if err != nil {
					log.Println(err)
				}

				//we've got a message from the router
			case routerMessage := <-messFromRouter:
				m2c.message = routerMessage
				err := conn.WriteJSON(m2c)
				if err != nil {
					log.Println(err)
				}
				//we've got a message from the client
			case clientMessage, q := <-messFromClient:
				if q == false {
					return
				}

				HandleMessage(clientMessage, conn, &telnetconn, quit, messFromRouter)

				/*****************************************************************************/
			}
		}
	}
	return http.HandlerFunc(fn)
}
