# JMDN-FastSync Interactive CLI Example

This example demonstrates how to use the PriorSync protocol with an interactive CLI.

## Building

```bash
cd example
go build -o priorsync-cli cli.go
```

## Running

Start the CLI:

```bash
./priorsync-cli
```

## Commands

### Start Listening

Start a node and listen for incoming PriorSync messages:

```
> listen 4001
```

Or use a random port:

```
> listen
```

### Send a Message

Send a PriorSync message to another peer:

```
> send /ip4/127.0.0.1/tcp/4001/p2p/QmPeerID SYNC_REQUEST
```

Available states:

- `SYNC_REQUEST` - Request synchronization
- `CHECKPOINT` - Send checkpoint data
- `RECONCILE` - Reconcile differences

### Check Status

View the current node status:

```
> status
```

### Quit

Exit the CLI:

```
> quit
```

## Example Session

**Terminal 1 (Node A):**

```
> listen 4001
Listening on: /ip4/127.0.0.1/tcp/4001/p2p/QmNodeA...
Peer ID: QmNodeA...

> status
Status: Running
Address: /ip4/127.0.0.1/tcp/4001/p2p/QmNodeA...
```

**Terminal 2 (Node B):**

```
> listen 4002
Listening on: /ip4/127.0.0.1/tcp/4002/p2p/QmNodeB...

> send /ip4/127.0.0.1/tcp/4001/p2p/QmNodeA... SYNC_REQUEST
Connected to peer: QmNodeA...
Sending PriorSync message with state: SYNC_REQUEST
✓ Message sent and acknowledged!
```

**Terminal 1 will show:**

```
Sync Request - LOG
```

## Notes

- The CLI uses libp2p for peer-to-peer communication
- Messages are sent using the PriorSync protocol
- The Datarouter handles incoming messages based on their state
- Each state (SYNC_REQUEST, CHECKPOINT, RECONCILE) can be extended with custom logic
