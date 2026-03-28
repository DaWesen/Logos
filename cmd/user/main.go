package main

import (
	"Noah/config"
	"Noah/internal/user/dao"
	"Noah/internal/user/handler"
	"Noah/internal/user/service"
	user "Noah/kitex_gen/user/userservice"
	"Noah/pkg/cache"
	"Noah/pkg/database/pgsql"
	"Noah/pkg/es"
	"Noah/pkg/jwt"
	"Noah/pkg/logger"
	"Noah/pkg/mq"
	"log"

	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
	etcd "github.com/kitex-contrib/registry-etcd"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	db, err := pgsql.InitPostgres()
	if err != nil {
		log.Fatalf("Failed to init postgres: %v", err)
	}

	userRepo := dao.NewUserRepository(db)

	jwtManager := jwt.NewJWTManager()

	var redisCache cache.Cache
	redisClient, err := cache.InitRedis(cfg.Redis)
	if err != nil {
		log.Printf("Failed to init redis, will continue without cache: %v", err)
	} else {
		redisCache = redisClient
	}

	var kafkaProducer *mq.Producer
	kafkaProducer = mq.NewProducer()

	var esManager *es.ESManager
	esClient, err := es.InitElasticsearch()
	if err != nil {
		log.Printf("Failed to init elasticsearch, will continue without es: %v", err)
	} else {
		esManager = es.NewESManager(esClient)
	}

	userService := service.NewUserService(userRepo, jwtManager, redisCache, kafkaProducer, esManager)

	r, err := etcd.NewEtcdRegistry(cfg.Etcd.Endpoints)
	if err != nil {
		log.Fatalf("Failed to create etcd registry: %v", err)
	}

	svr := user.NewServer(
		&handler.UserServiceImpl{UserService: userService},
		server.WithRegistry(r),
		server.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{ServiceName: "user"}),
	)

	if err := svr.Run(); err != nil {
		log.Fatalf("User service failed to run: %v", err)
	}
}
