require('dotenv').config();
const { Client } = require('pg');
const axios = require('axios');
const { BinTools } = require('avalanche');

// Load and validate environment variables
const requiredEnvVars = [
  'PGHOST',
  'PGPORT',
  'PGUSER',
  'PGPASSWORD',
  'PGDATABASE',
  'DATE_FILTER',
  'SUBGRAPH_URL',
  'VALIDATOR_RPC_URL',
];
const missingEnvVars = requiredEnvVars.filter(
  (varName) => !process.env[varName]
);
if (missingEnvVars.length > 0) {
  throw new Error(
    `Missing required environment variables: ${missingEnvVars.join(', ')}`
  );
}

// Database configuration from .env
const DB_CONFIG = {
  host: process.env.PGHOST,
  port: parseInt(process.env.PGPORT, 10),
  user: process.env.PGUSER,
  password: process.env.PGPASSWORD,
  database: process.env.PGDATABASE,
};

// Other configurations from .env
const FILTER_DATE = process.env.DATE_FILTER;
const SUBGRAPH_URL = process.env.SUBGRAPH_URL;
const VALIDATORS_API = process.env.VALIDATOR_RPC_URL;

// Initialize BinTools for cb58 conversions
const binTools = BinTools.getInstance();

// CB58 to Hex and Hex to CB58 conversion functions using avalanche
function cb58ToHex(cb58) {
  try {
    const buffer = binTools.cb58Decode(cb58);
    return buffer.toString('hex');
  } catch (error) {
    console.error(`CB58 conversion error for ${cb58}: ${error.message}`);
    throw new Error(`Failed to convert CB58 to hex: ${cb58}`);
  }
}

function hexToCb58(hex) {
  try {
    // Remove '0x' prefix if present
    const cleanHex = hex.replace(/^0x/, '');
    const buffer = Buffer.from(cleanHex, 'hex');
    return binTools.cb58Encode(buffer);
  } catch (error) {
    console.error(`Hex to CB58 conversion error for ${hex}: ${error.message}`);
    throw new Error(`Failed to convert hex to CB58: ${hex}`);
  }
}

// Initialize database client
const dbClient = new Client(DB_CONFIG);

async function main() {
  let GraphQLClient, gql;
  try {
    // Dynamically import graphql-request
    const graphqlRequest = await import('graphql-request');
    GraphQLClient = graphqlRequest.GraphQLClient;
    gql = graphqlRequest.gql;
  } catch (error) {
    throw new Error(`Failed to import graphql-request: ${error.message}`);
  }

  // Initialize GraphQL client
  const graphQLClient = new GraphQLClient(SUBGRAPH_URL);

  // GraphQL query for subgraph
  const VALIDATOR_QUERY = gql`
    query validatorRegistrations($id: String!) {
      validations(
        first: 1000
        where: { id: $id }
        orderBy: startedAt
        orderDirection: desc
      ) {
        id
        nodeID
        status
      }
    }
  `;

  try {
    // Connect to database
    await dbClient.connect();
    console.log('Connected to database');

    // Query database for uptime proofs not updated on FILTER_DATE
    const query = `
      SELECT validation_id, uptime_seconds
      FROM uptime_proofs
      WHERE updated_at::text NOT LIKE $1
    `;
    const result = await dbClient.query(query, [`${FILTER_DATE}%`]);

    // Arrays to store results
    const removedValidators = [];
    const uptimeComparisons = [];
    const processedNodeIDs = new Set(); // Track processed nodeIDs to avoid duplicates

    // Process each row
    for (const row of result.rows) {
      const { validation_id, uptime_seconds } = row;

      // Convert validation_id to hex
      let validationIdHex;
      try {
        validationIdHex = cb58ToHex(validation_id);
      } catch (error) {
        console.warn(
          `Skipping validation_id ${validation_id} due to CB58 conversion error`
        );
        continue;
      }

      // Query subgraph
      let validation;
      try {
        const subgraphData = await graphQLClient.request(VALIDATOR_QUERY, {
          id: validationIdHex,
        });
        validation = subgraphData.validations[0];
      } catch (error) {
        console.warn(
          `Subgraph query failed for validation_id ${validation_id}: ${error.message}`
        );
        continue;
      }

      if (!validation) {
        console.warn(
          `No subgraph data found for validation_id: ${validation_id}`
        );
        continue;
      }

      const { nodeID, status } = validation;
      let cb58NodeId;
      try {
        cb58NodeId = `NodeID-${hexToCb58(nodeID)}`;
      } catch (error) {
        console.warn(
          `Skipping nodeID ${nodeID} for validation_id ${validation_id} due to CB58 conversion error`
        );
        continue;
      }

      if (status === 'Removed') {
        // Compile inactive validators
        removedValidators.push(cb58NodeId);
      } else {
        // Skip if nodeID already processed
        if (processedNodeIDs.has(cb58NodeId)) {
          console.log(`Skipping duplicate nodeID: ${cb58NodeId}`);
          continue;
        }
        processedNodeIDs.add(cb58NodeId);

        // Query validators API for active validators
        let validator;
        try {
          const validatorsResponse = await axios.post(VALIDATORS_API, {
            jsonrpc: '2.0',
            method: 'validators.getCurrentValidators',
            params: { nodeIDs: [cb58NodeId] },
            id: 1,
          });

          // Log full response for debugging
          console.log(
            `Validators API response for nodeID ${cb58NodeId}:`,
            JSON.stringify(validatorsResponse.data, null, 2)
          );

          if (
            !validatorsResponse.data.result ||
            !validatorsResponse.data.result.validators
          ) {
            throw new Error('Invalid API response: missing result.validators');
          }

          validator = validatorsResponse.data.result.validators[0];
          if (!validator) {
            console.warn(`No validator data found for nodeID: ${cb58NodeId}`);
            continue;
          }
        } catch (error) {
          console.warn(
            `Validators API query failed for nodeID ${cb58NodeId}: ${error.message}`
          );
          continue;
        }

        const apiUptimeSeconds = validator.uptimeSeconds;
        const difference = apiUptimeSeconds - uptime_seconds;

        // Store comparison data
        uptimeComparisons.push({
          nodeID: cb58NodeId,
          lastUpdated: row.updated_at, // Note: updated_at not selected, adjust query if needed
          dbUptimeSeconds: uptime_seconds,
          apiUptimeSeconds: apiUptimeSeconds,
          difference,
        });
      }
    }

    // Output results
    console.log('\n=== Inactive Validators (Status: Removed) ===');
    if (removedValidators.length === 0) {
      console.log('No inactive validators found.');
    } else {
      removedValidators.forEach((nodeId) => console.log(`- ${nodeId}`));
    }

    console.log('\n=== Uptime Comparison for Active Validators ===');
    if (uptimeComparisons.length === 0) {
      console.log('No active validators to compare.');
    } else {
      uptimeComparisons.forEach((comp) => {
        console.log(`
Node ID: ${comp.nodeID}
Last Updated: ${
          comp.lastUpdated || 'N/A'
        } (Note: Add updated_at to query if needed)
Database Uptime (seconds): ${comp.dbUptimeSeconds}
API Uptime (seconds): ${comp.apiUptimeSeconds}
Difference (API - DB): ${comp.difference} seconds
        `);
      });
    }
  } catch (error) {
    console.error('Error:', error.message);
  } finally {
    // Close database connection
    await dbClient.end();
    console.log('Database connection closed');
  }
}

// Run the script
main();
