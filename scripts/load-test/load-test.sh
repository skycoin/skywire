#!/bin/bash

################################################################################
# Skywire Load Test Script
# 
# This script creates multiple Skywire visor containers for load testing and
# development purposes. Each container generates its own unique configuration
# on startup.
#
# Location: scripts/load-test.sh in skycoin/skywire repository
################################################################################

# Configuration variables
IMAGE_NAME="skycoin/skywire:test"          # Docker image to use
CONTAINER_PREFIX="skywire-visor"           # Prefix for container names (will be: skywire-visor-1, skywire-visor-2, etc.)

################################################################################
# Function: check_docker
# Description: Checks if Docker is installed and running. If not, offers to
#              install it automatically based on the detected operating system.
# Supports: Ubuntu/Debian, RedHat/CentOS/Fedora, macOS
################################################################################
check_docker() {
    # Check if docker command exists
    if ! command -v docker &> /dev/null; then
        echo "Docker is not installed on this system."
        read -p "Do you want to install Docker? (y/n): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            echo "Installing Docker..."
            
            # Detect operating system type
            if [[ "$OSTYPE" == "linux-gnu"* ]]; then
                # Linux installation - detect distribution
                if [ -f /etc/debian_version ]; then
                    # Debian/Ubuntu based systems
                    echo "Detected Debian/Ubuntu system"
                    sudo apt-get update
                    sudo apt-get install -y ca-certificates curl gnupg
                    sudo install -m 0755 -d /etc/apt/keyrings
                    
                    # Add Docker's official GPG key
                    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
                    sudo chmod a+r /etc/apt/keyrings/docker.gpg
                    
                    # Set up Docker repository
                    echo \
                      "deb [arch="$(dpkg --print-architecture)" signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
                      "$(. /etc/os-release && echo "$VERSION_CODENAME")" stable" | \
                      sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
                    
                    # Install Docker packages
                    sudo apt-get update
                    sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
                    
                elif [ -f /etc/redhat-release ]; then
                    # RedHat/CentOS/Fedora based systems
                    echo "Detected RedHat/CentOS/Fedora system"
                    sudo yum install -y yum-utils
                    sudo yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
                    sudo yum install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
                    
                    # Start and enable Docker service
                    sudo systemctl start docker
                    sudo systemctl enable docker
                else
                    echo "Unsupported Linux distribution"
                    exit 1
                fi
                
                # Add current user to docker group to run docker without sudo
                sudo usermod -aG docker $USER
                echo "Docker installed successfully!"
                echo "You may need to log out and log back in for group changes to take effect."
                echo "Or run: newgrp docker"
                
            elif [[ "$OSTYPE" == "darwin"* ]]; then
                # macOS installation
                echo "Detected macOS system"
                if command -v brew &> /dev/null; then
                    # Install via Homebrew if available
                    echo "Installing Docker via Homebrew..."
                    brew install --cask docker
                    echo "Docker installed! Please start Docker Desktop from Applications."
                    echo "After Docker Desktop is running, re-run this script."
                    exit 0
                else
                    # Homebrew not available, provide manual instructions
                    echo "Homebrew is not installed. Please install Docker Desktop manually from:"
                    echo "https://www.docker.com/products/docker-desktop"
                    exit 1
                fi
            else
                # Unsupported OS
                echo "Unsupported operating system: $OSTYPE"
                echo "Please install Docker manually from: https://docs.docker.com/get-docker/"
                exit 1
            fi
        else
            echo "Docker is required to run this script. Exiting."
            exit 1
        fi
    else
        # Docker is already installed, show version
        echo "Docker is already installed: $(docker --version)"
    fi
    
    # Check if Docker daemon is running
    if ! docker info &> /dev/null; then
        echo "Docker is installed but the Docker daemon is not running."
        if [[ "$OSTYPE" == "linux-gnu"* ]]; then
            # Try to start Docker service on Linux
            echo "Starting Docker service..."
            sudo systemctl start docker
            sudo systemctl enable docker
        else
            # On macOS, user needs to start Docker Desktop manually
            echo "Please start Docker Desktop and re-run this script."
            exit 1
        fi
    fi
}

# Run Docker check before proceeding
check_docker

################################################################################
# Argument Parsing
# Parse command line arguments for the 'start' command
################################################################################
if [ "$1" != "stop" ] && [ "$1" != "clean" ] && [ "$1" != "restart" ] && [ "$1" != "status" ] && [ "$1" != "stats" ] && [ "$1" != "logs" ] && [ "$1" != "count" ] && [ "$1" != "summary" ]; then
    if [ "$1" == "start" ]; then
        # Validate that both number of containers and delay are provided
        if [ -z "$2" ] || [ -z "$3" ]; then
            echo "Usage: $0 start <number_of_containers> <delay_seconds>"
            echo "Example: $0 start 200 10"
            exit 1
        fi
        NUM_CONTAINERS=$2    # Number of containers to start
        DELAY_SECONDS=$3     # Delay in seconds between starting each container
    fi
fi

################################################################################
# Main Command Handler
# Processes the command provided by the user
################################################################################
case "${1:-help}" in
    #---------------------------------------------------------------------------
    # START: Launch multiple Skywire containers
    #---------------------------------------------------------------------------
    start)
        echo "Starting $NUM_CONTAINERS Skywire containers with ${DELAY_SECONDS}s delay..."
        
        # Pull the latest image before starting containers
        echo "Pulling Docker image: $IMAGE_NAME"
        docker pull "$IMAGE_NAME"
        if [ $? -ne 0 ]; then
            echo "Failed to pull Docker image. Exiting."
            exit 1
        fi
        echo ""
        
        # Loop to create each container
        for i in $(seq 1 $NUM_CONTAINERS); do
            container_name="${CONTAINER_PREFIX}-${i}"
            
            echo "[$i/$NUM_CONTAINERS] Starting container: $container_name"
            
            # Run container with:
            # - Unique name for each instance
            # - Restart policy set to 'always' for automatic recovery
            # - Override entrypoint to run shell commands
            # - Generate config inside container before starting visor
            docker run -d \
                --name "$container_name" \
                --restart always \
                --entrypoint sh \
                "$IMAGE_NAME" \
                -c "/release/skywire cli config gen -t -o /tmp/config.json && /release/skywire visor -c /tmp/config.json"
            
            # Wait before starting next container to avoid overwhelming the system
            echo "Waiting ${DELAY_SECONDS} seconds..."
            sleep $DELAY_SECONDS
        done
        
        echo "All $NUM_CONTAINERS containers started!"
        ;;
    
    #---------------------------------------------------------------------------
    # STOP: Stop all running Skywire containers (but don't remove them)
    #---------------------------------------------------------------------------
    stop)
        echo "Stopping all Skywire containers..."
        # Find all containers matching our prefix and stop them
        docker stop $(docker ps -q --filter "name=${CONTAINER_PREFIX}-") 2>/dev/null || echo "No containers to stop"
        echo "Done!"
        ;;
    
    #---------------------------------------------------------------------------
    # CLEAN: Stop and remove all Skywire containers
    #---------------------------------------------------------------------------
    clean)
        echo "Stopping and removing all Skywire containers..."
        # Force remove all containers (stops them if running)
        docker rm -f $(docker ps -aq --filter "name=${CONTAINER_PREFIX}-") 2>/dev/null || echo "No containers to remove"
        echo "Done!"
        ;;
    
    #---------------------------------------------------------------------------
    # RESTART: Restart all Skywire containers
    #---------------------------------------------------------------------------
    restart)
        echo "Restarting all Skywire containers..."
        # Restart all matching containers
        docker restart $(docker ps -q --filter "name=${CONTAINER_PREFIX}-") 2>/dev/null || echo "No containers to restart"
        echo "Done!"
        ;;
    
    #---------------------------------------------------------------------------
    # STATUS: Show status of all Skywire containers
    #---------------------------------------------------------------------------
    status)
        echo "Skywire container status:"
        # Display container names, status, and uptime in table format
        docker ps -a --filter "name=${CONTAINER_PREFIX}-" --format "table {{.Names}}\t{{.Status}}\t{{.RunningFor}}"
        ;;
    
    #---------------------------------------------------------------------------
    # STATS: Show resource usage for all containers
    #---------------------------------------------------------------------------
    stats)
        echo "Skywire container resource usage:"
        # Get list of running container names
        container_names=$(docker ps --filter "name=${CONTAINER_PREFIX}-" --format "{{.Names}}" | tr '\n' ' ')
        if [ -n "$container_names" ]; then
            # Display CPU, memory, network, and block I/O stats
            docker stats --no-stream $container_names
        else
            echo "No running containers found"
        fi
        ;;
    
    #---------------------------------------------------------------------------
    # LOGS: Follow logs of a specific container
    #---------------------------------------------------------------------------
    logs)
        # Validate container number is provided
        if [ -z "$2" ]; then
            echo "Usage: $0 logs <container_number>"
            echo "Example: $0 logs 5"
            exit 1
        fi
        # Follow logs in real-time with -f flag
        docker logs -f "${CONTAINER_PREFIX}-$2"
        ;;
    
    #---------------------------------------------------------------------------
    # COUNT: Show quick count of running vs total containers
    #---------------------------------------------------------------------------
    count)
        # Count running containers
        running=$(docker ps -q --filter "name=${CONTAINER_PREFIX}-" | wc -l)
        # Count all containers (including stopped)
        total=$(docker ps -aq --filter "name=${CONTAINER_PREFIX}-" | wc -l)
        echo "Running: $running / Total: $total"
        ;;
    
    #---------------------------------------------------------------------------
    # SUMMARY: Show aggregated resource usage report
    #---------------------------------------------------------------------------
    summary)
        echo "=== Skywire Containers Summary Report ==="
        echo ""
        
        # Calculate container counts
        running=$(docker ps -q --filter "name=${CONTAINER_PREFIX}-" | wc -l)
        total=$(docker ps -aq --filter "name=${CONTAINER_PREFIX}-" | wc -l)
        stopped=$((total - running))
        
        # Display container statistics
        echo "Containers:"
        echo "  Running: $running"
        echo "  Stopped: $stopped"
        echo "  Total: $total"
        echo ""
        
        # Exit if no containers are running
        if [ $running -eq 0 ]; then
            echo "No running containers to analyze"
            exit 0
        fi
        
        # Collect stats from all running containers
        echo "Collecting stats..."
        container_names=$(docker ps --filter "name=${CONTAINER_PREFIX}-" --format "{{.Names}}" | tr '\n' ' ')
        stats=$(docker stats --no-stream $container_names)
        
        echo ""
        echo "=== Aggregated Resources ==="
        
        # Calculate total CPU usage across all containers
        # Removes '%' sign and sums all values
        total_cpu=$(echo "$stats" | tail -n +2 | awk '{gsub(/%/,"",$3); sum+=$3} END {printf "%.2f%%", sum}')
        echo "  Total CPU: $total_cpu"
        
        # Calculate total memory usage
        # Handles both MiB and GiB units, converts GiB to MiB for accurate sum
        total_mem=$(echo "$stats" | tail -n +2 | awk '{
            split($4,a,/MiB|GiB/); 
            mem=a[1]; 
            if($4 ~ /GiB/) mem*=1024; 
            sum+=mem
        } END {
            if(sum >= 1024) printf "%.2f GiB", sum/1024; 
            else printf "%.2f MiB", sum
        }')
        echo "  Total Memory: $total_mem"
        
        # Calculate average memory per container
        avg_mem=$(echo "$stats" | tail -n +2 | awk -v count=$running '{
            split($4,a,/MiB|GiB/); 
            mem=a[1]; 
            if($4 ~ /GiB/) mem*=1024; 
            sum+=mem
        } END {printf "%.2f MiB", sum/count}')
        echo "  Avg Memory/Container: $avg_mem"
        
        echo ""
        ;;
    
    #---------------------------------------------------------------------------
    # HELP: Display usage information
    #---------------------------------------------------------------------------
    help|*)
        echo "Usage: $0 {start|stop|clean|restart|status|stats|logs|count|summary}"
        echo ""
        echo "Commands:"
        echo "  start <num> <delay>  - Start specified number of containers with delay"
        echo "                         Example: $0 start 200 10"
        echo "  stop                 - Stop all containers"
        echo "  clean                - Stop and remove all containers"
        echo "  restart              - Restart all containers"
        echo "  status               - Show status of all containers"
        echo "  stats                - Show resource usage of all containers"
        echo "  logs <N>             - Follow logs of container N"
        echo "  count                - Show running/total count"
        echo "  summary              - Show aggregated resource usage report"
        exit 1
        ;;
esac
