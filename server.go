package master

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/codeuniversity/al-master/metrics"
	"github.com/codeuniversity/al-master/websocket"
	"github.com/codeuniversity/al-proto"
	websocketConn "github.com/gorilla/websocket"
	"google.golang.org/grpc"
)

const (
	statesFolderName = "states"
)

//ServerConfig contains config data for Server
type ServerConfig struct {
	ConnBufferSize    int
	GRPCPort          int
	HTTPPort          int
	StateFileName     string
	LoadLatestState   bool
	BigBangConfigPath string
	BucketWidth       int
}

//Server that manages cell changes
type Server struct {
	ServerConfig

	*SimulationState

	cisClientPool               *CISClientPool
	websocketConnectionsHandler *websocket.ConnectionsHandler

	grpcServer *grpc.Server
	httpServer *http.Server
}

//NewServer with given config
func NewServer(config ServerConfig) *Server {
	clientPool := NewCISClientPool(config.ConnBufferSize)

	return &Server{
		ServerConfig:                config,
		websocketConnectionsHandler: websocket.NewConnectionsHandler(),
		cisClientPool:               clientPool,
	}
}

//Init loads state from a file or by asking a cis instance for a new BigBang depending on ServerConfig
func (s *Server) Init() {
	s.initPrometheus()
	go s.listen()

	if s.StateFileName != "" {
		simulationState, err := LoadSimulationState(filepath.Join(statesFolderName, s.StateFileName))
		if err != nil {
			fmt.Println("\nLoading state from filepath failed, exiting now", err)
			panic(err)
		}
		s.SimulationState = simulationState
		return
	}

	if s.LoadLatestState {
		simulationState, err := LoadLatestSimulationState()
		if err != nil {
			fmt.Println("\nLoading latest state failed, exiting now", err)
			panic(err)
		}
		s.SimulationState = simulationState
		return
	}

	s.fetchBigBang()
}

//Run offloads the computation of changes to cis
func (s *Server) Run() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	for {
		if len(s.CellBuckets.AllCells()) == 0 {
			fmt.Println("no cells remaining, stopping...")
			s.closeConnections()
			return
		}

		if len(signals) != 0 {
			fmt.Println("Received Signal:", <-signals)
			break
		}
		s.step()
	}
	s.shutdown()
}

//Register cis-slave and create clients to make the slave useful
func (s *Server) Register(ctx context.Context, registration *proto.SlaveRegistration) (*proto.SlaveRegistrationResponse, error) {
	for i := 0; i < int(registration.Threads); i++ {
		client, err := createCellInteractionClient(registration.Address)
		if err != nil {
			return nil, err
		}
		s.cisClientPool.AddClient(client)
		metrics.CISClientCount.Inc()
	}
	return &proto.SlaveRegistrationResponse{}, nil
}

func (s *Server) initPrometheus() {
	prometheus.MustRegister(metrics.AmountOfBuckets)
	prometheus.MustRegister(metrics.AverageCellsPerBucket)
	prometheus.MustRegister(metrics.MedianCellsPerBucket)
	prometheus.MustRegister(metrics.MinCellsInBuckets)
	prometheus.MustRegister(metrics.MaxCellsInBuckets)
	prometheus.MustRegister(metrics.CISCallCounter)
	prometheus.MustRegister(metrics.CisCallDurationSeconds)
	prometheus.MustRegister(metrics.CISClientCount)
	prometheus.MustRegister(metrics.WebSocketConnectionsCount)

	http.Handle("/metrics", promhttp.Handler())
}

func (s *Server) shutdown() {
	s.closeConnections()

	err := s.saveState()
	if err == nil {
		fmt.Println("\nState successfully saved")
	} else {
		fmt.Println("\nState could not be saved:", err)
	}
}

func (s *Server) closeConnections() {
	s.websocketConnectionsHandler.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := s.httpServer.Shutdown(ctx)
	if err != nil {
		fmt.Println("Couldn't shutdown http server", err)
	}
	s.grpcServer.Stop()
}

func (s *Server) fetchBigBang() {
	config, err := BigBangConfigFromPath(s.ServerConfig.BigBangConfigPath)
	if err != nil {
		panic(err)
	}
	c := s.cisClientPool.GetClient()
	defer s.cisClientPool.AddClient(c)
	withTimeout(100*time.Second, func(ctx context.Context) {
		stream, err := c.BigBang(ctx, config.ToProto())
		if err != nil {
			panic(err)
		}
		cells := make([]*proto.Cell, 0, config.CellAmount)
		for {
			cell, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					log.Fatal(err)
				}
				break
			}
			cells = append(cells, cell)
		}
		buckets := CreateBuckets(cells, uint(s.BucketWidth))
		s.SimulationState = NewSimulationState(buckets)
	})
}

func (s *Server) listen() {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%v", s.GRPCPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s.grpcServer = grpc.NewServer()
	proto.RegisterSlaveRegistrationServiceServer(s.grpcServer, s)

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	http.HandleFunc("/", s.websocketHandler)
	s.httpServer = &http.Server{Addr: fmt.Sprintf(":%v", s.HTTPPort), Handler: nil}
	if err := s.httpServer.ListenAndServe(); err != nil {
		log.Println(err)
	}
}

var upgrader = websocketConn.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(_ *http.Request) bool {
		return true
	},
}

func (s *Server) websocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	s.websocketConnectionsHandler.AddConnection(conn)
}

func (s *Server) broadcastCurrentState() {
	s.websocketConnectionsHandler.BroadcastCells(s.CellBuckets.AllCells())
}

func (s *Server) step() {
	UpdateBucketsMetrics(s.CellBuckets)
	doneChan := make(chan struct{})

	go s.processReturnedBatches(s.CurrentReturnedBatchChan(), doneChan)
	for key, bucket := range s.CellBuckets {
		if s.RequestInflightFromLastStep(key) {
			continue
		}

		surroundingCells := []*proto.Cell{}
		for _, otherKey := range key.SurroundingKeys(s.BucketWidth) {
			if otherBucket, ok := s.CellBuckets[otherKey]; ok {
				surroundingCells = append(surroundingCells, otherBucket...)
			}
		}
		s.CurrentWaitGroup().Add(1)
		batch := &proto.CellComputeBatch{
			CellsToCompute:   bucket,
			CellsInProximity: surroundingCells,
			TimeStep:         s.TimeStep,
			BatchKey:         string(key),
		}
		go s.callCIS(batch, s.CurrentWaitGroup(), s.CurrentReturnedBatchChan())
	}

	s.CurrentWaitGroup().Wait()
	s.Cycle()
	<-doneChan
	s.TimeStep++
	fmt.Println(s.TimeStep, ": ", len(s.CellBuckets.AllCells()))
	s.broadcastCurrentState()
}

func (s *Server) callCIS(batch *proto.CellComputeBatch, wg *sync.WaitGroup, returnedBatchChan chan *proto.CellComputeBatch) {
	metrics.CISCallCounter.Inc()
	looping := true
	for looping {
		c := s.cisClientPool.GetClient()
		withTimeout(10*time.Second, func(ctx context.Context) {
			start := time.Now()
			returnedBatch, err := c.ComputeCellInteractions(ctx, batch)
			metrics.CisCallDurationSeconds.Observe(time.Since(start).Seconds())
			if err == nil {
				s.cisClientPool.AddClient(c)
				returnedBatchChan <- returnedBatch
				looping = false
			} else {
				metrics.CISClientCount.Dec()
			}
		})
	}
	wg.Done()
}

func (s *Server) processReturnedBatches(returnedBatchChan chan *proto.CellComputeBatch, doneChan chan struct{}) {
	nextBuckets := Buckets{}
	doneNeighbourBuckets := map[BucketKey]int{}

	for returnedBatch := range returnedBatchChan {
		returnedBuckets := CreateBuckets(returnedBatch.CellsToCompute, uint(s.BucketWidth))
		nextBuckets.Merge(returnedBuckets)
		bucketKey := BucketKey(returnedBatch.BatchKey)

		// call cis for next step if possible
		keysToCheck := append(bucketKey.SurroundingKeys(s.BucketWidth), bucketKey)

		for _, key := range keysToCheck {
			doneNeighbourBuckets[key]++
			bucket, exists := nextBuckets[key]
			if !exists {
				continue
			}
			if len(bucket) == 0 || doneNeighbourBuckets[key] < 27 || s.RequestInflight(key) {
				continue
			}

			surroundingCells := []*proto.Cell{}
			for _, surroundingKey := range key.SurroundingKeys(s.BucketWidth) {
				surroundingBucket := nextBuckets[surroundingKey]
				surroundingCells = append(surroundingCells, surroundingBucket...)
			}

			batch := &proto.CellComputeBatch{
				CellsToCompute:   bucket,
				CellsInProximity: surroundingCells,
				TimeStep:         s.TimeStep + 1,
				BatchKey:         string(key),
			}
			s.NextWaitGroup().Add(1)
			go s.callCIS(batch, s.NextWaitGroup(), s.NextReturnedBatchChan())

			s.MarkRequestInflight(key)
		}
	}
	s.CellBuckets = nextBuckets
	doneChan <- struct{}{}
}

func withTimeout(timeout time.Duration, f func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	f(ctx)
}

func createCellInteractionClient(address string) (proto.CellInteractionServiceClient, error) {
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	return proto.NewCellInteractionServiceClient(conn), nil
}
