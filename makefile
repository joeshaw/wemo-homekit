# Go parameters
    GOCMD=go
    GOBUILD=$(GOCMD) build
    GOCLEAN=$(GOCMD) clean
    GOTEST=$(GOCMD) test
    GOGET=$(GOCMD) get
    TARGET=bin
    BINARY_NAME=wemo-homecontrol
    
# Docker parameters
    DOCKERCMD=sudo docker
    DOCKERBUILD=$(DOCKERCMD) build
    DOCKERRUN=$(DOCKERCMD) run
    DOCKERNAME=wemo-homecontrol
    DOCKERVOLUMEDATA=/srv/docker/wemo-homecontrol/data

    
    all: test build
    build: 
		$(GOBUILD) -o $(TARGET)/$(BINARY_NAME) -v
    test:
		$(GOTEST) -v ./...
    clean:
		$(GOCLEAN)
		rm -f $(TARGET)/$(BINARY_NAME)            
    run:
		$(GOBUILD) -o $(TARGET)/$(BINARY_NAME) -v ./...
		./$(TARGET)/$(BINARY_NAME)
    deps:
            
    
    
    # Cross compilation
    build-docker:
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -a -installsuffix cgo -o $(TARGET)/$(BINARY_NAME) -v
	
	#Docker container build
    docker-build:
		$(DOCKERBUILD) -t $(BINARY_NAME) -f Dockerfile .

	#Docker run
    docker-run:
		$(DOCKERRUN) --name $(DOCKERNAME) --rm --network host -v $(DOCKERVOLUMEDATA):/data $(BINARY_NAME)