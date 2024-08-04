<p align='center'> 
  <img src="https://github.com/user-attachments/assets/11fc9583-0460-4b18-bf2e-3412f67f76ba" alt="logo">
  <h1 align="center">pub-sub</h1>
  <h2 align="center">Broadcast your messagages at ease</h2>
</p>

## About the Project
# Broadcasting Mircoservices

This Go-based microservice facilitates efficient, large-scale message broadcasting from a single sender to millions of receivers. It's designed to operate with minimal resources, making it highly scalable and cost-effective. The service acts as a central hub, managing connections between one sender client and a vast number of receiver clients. When the sender broadcasts a message, the microservice efficiently distributes it to all connected receivers simultaneously. By leveraging Go's concurrency and OS level epoll features optimizing resource usage, this solution enables real-time communication for applications requiring widespread message dissemination while maintaining low infrastructure demands.

### Built With

- [go](https://go.dev/)

## Usage

1. Clone this repo.
2. start Server by running `go run server.go`.
3. Create a sever connection `ws://localhost:8000/sender`.
4. Connect as many clients posisble `ws://localhost:8000s/receiver`.
5. Start sending meaages to all clients from sender connection.
