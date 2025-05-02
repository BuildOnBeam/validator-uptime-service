require('dotenv').config();
const { Client } = require('pg');
const axios = require('axios');
const { BinTools } = require('avalanche');
const fs = require('fs');

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
  const startTime = performance.now(); // Start timer

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

    // Log DATE_FILTER for debugging
    console.log(`Using DATE_FILTER: ${FILTER_DATE}`);

    // Debug: Query all updated_at values to inspect data
    const debugQuery = `
      SELECT validation_id, updated_at, DATE(updated_at AT TIME ZONE 'UTC') as utc_date
      FROM uptime_proofs
      LIMIT 10
    `;
    const debugResult = await dbClient.query(debugQuery);
    console.log(
      'Sample updated_at values from uptime_proofs (before filtering):'
    );
    debugResult.rows.forEach((row) => {
      console.log(
        `- validation_id: ${row.validation_id}, updated_at: ${row.updated_at}, UTC Date: ${row.utc_date}`
      );
    });

    // Query database for uptime proofs not updated on FILTER_DATE
    const query = `
      SELECT validation_id, uptime_seconds, updated_at
      FROM uptime_proofs
      WHERE DATE(updated_at AT TIME ZONE 'UTC')::text NOT LIKE $1
    `;
    const filterParam = `${FILTER_DATE}%`;
    console.log(`Query filter parameter: ${filterParam}`);
    const result = await dbClient.query(query, [filterParam]);

    // Log sample updated_at values after filtering
    if (result.rows.length > 0) {
      console.log('Sample updated_at values from query (after filtering):');
      result.rows.slice(0, 3).forEach((row) => {
        console.log(
          `- ${row.updated_at} (UTC Date: ${
            new Date(row.updated_at).toISOString().split('T')[0]
          })`
        );
      });
    } else {
      console.log('No rows returned from uptime_proofs query.');
    }

    // Debug: Check for any 2025-04-30 dates that passed the filter
    const unexpectedDates = result.rows.filter((row) =>
      row.updated_at.toISOString().startsWith('2025-04-30')
    );
    if (unexpectedDates.length > 0) {
      console.warn('Unexpected 2025-04-30 dates found in query results:');
      unexpectedDates.forEach((row) => {
        console.warn(
          `- validation_id: ${row.validation_id}, updated_at: ${row.updated_at}`
        );
      });
    }

    // Arrays to store results
    const removedValidators = [];
    const uptimeComparisons = [];
    const processedNodeIDs = new Set(); // Track processed nodeIDs to avoid duplicates

    // Process each row
    for (const row of result.rows) {
      const { validation_id, uptime_seconds, updated_at } = row;

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

      // Skip if nodeID already processed
      if (processedNodeIDs.has(cb58NodeId)) {
        console.log(`Skipping duplicate nodeID: ${cb58NodeId}`);
        continue;
      }
      processedNodeIDs.add(cb58NodeId);

      if (status === 'Removed') {
        // Compile inactive validators for logging
        removedValidators.push(cb58NodeId);
        // Add to uptimeComparisons with null API data
        uptimeComparisons.push({
          nodeId: cb58NodeId,
          nodeIdHex: nodeID.replace(/^0x/, ''),
          lastUpdated: updated_at,
          databaseUptime: uptime_seconds,
          apiUptime: null,
          differenceUptime: null,
          status: 'Removed',
        });
      } else {
        // Query validators API for active validators
        let validator;
        try {
          const validatorsResponse = await axios.post(VALIDATORS_API, {
            jsonrpc: '2.0',
            method: 'validators.getCurrentValidators',
            params: {},
            id: 1,
          });

          if (
            !validatorsResponse.data.result ||
            !validatorsResponse.data.result.validators
          ) {
            throw new Error('Invalid API response: missing result.validators');
          }

          // Find the validator matching cb58NodeId
          validator = validatorsResponse.data.result.validators.find(
            (v) => v.nodeID === cb58NodeId
          );

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

        // Add to uptimeComparisons with API data
        uptimeComparisons.push({
          nodeId: cb58NodeId,
          nodeIdHex: nodeID.replace(/^0x/, ''),
          lastUpdated: updated_at,
          databaseUptime: uptime_seconds,
          apiUptime: apiUptimeSeconds,
          differenceUptime: difference,
          status: 'Active',
        });
      }
    }

    // Output inactive node IDs in a single log
    console.log(
      '\nInactive Node IDs:',
      removedValidators.length > 0
        ? `[${removedValidators.join(', ')}]`
        : 'None'
    );

    // Output uptime comparisons for active validators
    console.log('\n=== Active Validators (Status: Active) ===');
    const activeValidators = uptimeComparisons.filter(
      (comp) => comp.status === 'Active'
    );
    if (activeValidators.length === 0) {
      console.log('No active validators to compare.');
    } else {
      activeValidators.forEach((comp) => {
        console.log(`
Node ID: ${comp.nodeId}
Node ID (Hex): ${comp.nodeIdHex}
Last Updated: ${comp.lastUpdated}
Database Uptime (seconds): ${comp.databaseUptime}
API Uptime (seconds): ${comp.apiUptime}
Difference (API - DB): ${comp.differenceUptime} seconds
Status: ${comp.status}
        `);
      });
    }

    // Write reports to JSON files
    try {
      const timestamp = new Date().toISOString().replace(/[:.]/g, '-');

      // Removed validators report
      const removedValidatorsReport = uptimeComparisons.filter(
        (comp) => comp.status === 'Removed'
      );
      const removedFilename = `uptime_report_${timestamp}_removed.json`;
      fs.writeFileSync(
        removedFilename,
        JSON.stringify(removedValidatorsReport, null, 2)
      );
      console.log(`Removed validators report saved to ${removedFilename}`);

      // Active validators (discrepancy) report
      const activeValidatorsReport = uptimeComparisons.filter(
        (comp) => comp.status === 'Active'
      );
      const discrepancyFilename = `uptime_report_${timestamp}_discrepancy.json`;
      fs.writeFileSync(
        discrepancyFilename,
        JSON.stringify(activeValidatorsReport, null, 2)
      );
      console.log(`Discrepancy report saved to ${discrepancyFilename}`);
    } catch (error) {
      console.error(`Failed to write report to JSON file: ${error.message}`);
    }
  } catch (error) {
    console.error('Error:', error.message);
  } finally {
    // Close database connection
    await dbClient.end();
    console.log('Database connection closed');

    // Log execution time
    const endTime = performance.now();
    const duration = (endTime - startTime) / 1000; // Convert to seconds
    console.log(`Script execution time: ${duration.toFixed(3)} seconds`);
  }
}

// Run the script
main();
