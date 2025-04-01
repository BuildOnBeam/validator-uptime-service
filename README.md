# Validator Uptime Service

A service that automates the submission of validator uptime proofs and manages delegation rewards on the Avalanche network and Beam subnet. This service helps maintain validator rewards by regularly submitting signed uptime proofs and resolving delegation rewards.

## Overview

1. **Fetch Validator Uptimes**: Retrieves current validator uptime information from the Avalanche P-Chain
2. **Generate Uptime Proofs**: Creates and optimizes uptime proof messages by finding the maximum provable uptime
3. **Signature Aggregation**: Aggregates signatures for uptime proofs through the signature aggregation service
4. **Submit Proofs**: Submits signed uptime proofs to the staking manager contract
5. **Resolve Delegation Rewards**: Processes delegation rewards for validators with successful uptime submissions

The service runs a continuous loop with a 24-hour cycle to automate these operations.

## Prerequisites

- Go 1.23.7 or higher
- Access to an Avalanche node
- Access to a signature aggregator service
- Beam network credentials (private key)
- GraphQL endpoint for delegation data

## Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/yourusername/uptime-service.git
   cd uptime-service
   ```

2. Build the service:
   ```bash
   go build -o uptime-service
   ```

## Configuration

Create a `config.json` file in the same directory as the executable with the following structure:

```json
{
  "avalanche_api": "https://api.avax.network",
  "aggregator_url": "https://aggregator.example.com",
  "graphql_endpoint": "https://graph.onbeam.com/subgraphs/name/pos-testnet/graphql",
  "signing_subnet_id": "2JVSBoinj9C2J33VntvzYtVJNZdN2NKiwwKjcumHUWEb5DbBrm",
  "source_chain_id": "yH8D7ThNJkxmtkuv2jgBa4P1Rn3Qpr4pPr7QYNfcdoS6k6HWp",
  "quorum_percentage": 67,
  "beam_rpc": "https://eu.build.onbeam.com/rpc/testnet/your-api-key",
  "contract_address": "0x1234567890123456789012345678901234567890",
  "warp_messenger_address": "0x0987654321098765432109876543210987654321",
  "private_key": "your-private-key",
  "log_level": "info",
  "network_id": 1
}
```

### Configuration Parameters

| Parameter | Description |
|-----------|-------------|
| `avalanche_api` | Endpoint for the Avalanche API |
| `aggregator_url` | URL of the signature aggregator service |
| `graphql_endpoint` | GraphQL endpoint for fetching delegation data |
| `signing_subnet_id` | ID of the subnet responsible for signing messages |
| `source_chain_id` | Blockchain ID from which messages originate |
| `quorum_percentage` | Required percentage for signature quorum (default: 67) |
| `beam_rpc` | RPC endpoint for the Beam network |
| `contract_address` | Address of the staking manager contract |
| `warp_messenger_address` | Address of the warp messenger contract |
| `private_key` | Private key for transaction signing |
| `log_level` | Logging level (info/error) |
| `network_id` | Network ID (1 for Mainnet, 5 for Fuji Testnet) |

## Usage

Run the service with:

```bash
./uptime-service
```

## Technical Architecture

The service implements a modular architecture with the following components:

### AggregatorClient
- Establishes connection to the Subnet's signature aggregation service
- Implements `PackValidationUptimeMessage()` which generates a 46-byte uptime proof message using the Warp protocol
- Uses `SubmitAggregateRequest()` to send unsigned messages and retrieve signatures that meet quorum requirements

### ContractClient
- Handles the EVM contract communication via raw transaction assembly
- Implements `SubmitUptimeProof()` which formats and transmits uptime proofs to the staking manager contract
- Uses `TxToMethodWithWarpMessage()` to construct transactions containing Warp protocol messages

### DelegationClient
- Implements a GraphQL query interface via `GetDelegationsForValidator()`
- Contains batch processing logic for large delegation sets in `ResolveRewards()`
- Manages transaction nonce handling and gas optimization
- Implements error handling with backoff for failed rewards resolution

### Communication Flow
```
┌────────────┐     ┌───────────────┐     ┌──────────────┐
│ Avalanche  │     │  Aggregator   │     │    Beam      │
│  P-Chain   │◄────┤    Service    │◄────┤   Network    │
└────────────┘     └───────────────┘     └──────────────┘
       ▲                   ▲                    ▲
       │                   │                    │
       └───────────────┬───────────────────────┘
                       │
                   ┌───┴───┐
                   │ Uptime │
                   │Service │
                   └───────┘
```