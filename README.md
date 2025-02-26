# Avalanche Validator Uptime Service

Service that monitors Avalanche validator uptimes, aggregates signatures, and submits proofs to the validator manager smart contract.

## Installation

```bash
git clone https://github.com/neobase-one/uptime-service/
cd uptime-serice
npm install
```

## Configuration Parameters

Create a `config.json` file in the root directory with the following params:

- `avalanche_api`: Endpoint for the Avalanche P-Chain API
- `aggregator_url`: URL of the signature aggregation service
- `signing_subnet_id`: ID of the subnet for signature aggregation
- `quorum_percentage`: Required percentage for signature quorum (default: 67)
- `beam_rpc`: RPC endpoint for the Beam network
- `contract_address`: Address of the staking manager contract
- `private_key`: Private key for contract interactions
- `log_level`: Logging level ("info" or "error")
- `network_id`: Network identifier
- `source_chain_id`: Source chain identifier

## Running the Service

Development mode:
```bash
npm run dev
```

Production mode:
```bash
npm run build
npm start
```