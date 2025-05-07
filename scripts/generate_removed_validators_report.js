import fs from 'fs';

// Function to escape CSV values (handle commas, quotes, etc.)
function escapeCsv(value) {
  if (value == null) return '';
  const str = String(value);
  if (str.includes(',') || str.includes('"') || str.includes('\n')) {
    return `"${str.replace(/"/g, '""')}"`;
  }
  return str;
}

// Read the removed_validators_only.json file
let jsonData;
try {
  const rawData = fs.readFileSync('removed_validators_only.json', 'utf8');
  jsonData = JSON.parse(rawData);
} catch (err) {
  console.error(
    `Error reading or parsing removed_validators_only.json: ${err.message}`
  );
  process.exit(1);
}

// Extract validators array
const validators = jsonData.validators || [];
if (!Array.isArray(validators)) {
  console.error('No validators array found in removed_validators_only.json');
  process.exit(1);
}

// Prepare CSV headers
const headers = [
  'owner_address',
  'fee_ratio_primary',
  'fee_ratio_secondary',
  'reward_ratio_primary',
  'reward_ratio_secondary',
];

// Collect unique owners and aggregate data
const ownerData = new Map();

validators.forEach((validator) => {
  // Initialize validator owner
  const validatorOwner = validator.owner;
  if (!ownerData.has(validatorOwner)) {
    ownerData.set(validatorOwner, {
      fee_ratio_primary: 0,
      fee_ratio_secondary: 0,
      reward_ratio_primary: 0,
      reward_ratio_secondary: 0,
    });
  }
  const validatorOwnerData = ownerData.get(validatorOwner);
  validatorOwnerData.fee_ratio_primary += validator.feeRatioPrimary || 0;
  validatorOwnerData.fee_ratio_secondary += validator.feeRatioSecondary || 0;

  // Process delegators
  validator.delegators.forEach((delegator) => {
    const delegatorOwner = delegator.owner;
    if (!ownerData.has(delegatorOwner)) {
      ownerData.set(delegatorOwner, {
        fee_ratio_primary: 0,
        fee_ratio_secondary: 0,
        reward_ratio_primary: 0,
        reward_ratio_secondary: 0,
      });
    }
    const data = ownerData.get(delegatorOwner);
    if (delegator.type === 'primary') {
      data.reward_ratio_primary += delegator.rewardRatio || 0;
    } else if (delegator.type === 'secondary') {
      data.reward_ratio_secondary += delegator.rewardRatio || 0;
    }
  });
});

// Prepare CSV rows
const rows = Array.from(ownerData.entries()).map(([owner, data]) => [
  escapeCsv(owner),
  escapeCsv(data.fee_ratio_primary.toFixed(18)),
  escapeCsv(data.fee_ratio_secondary.toFixed(18)),
  escapeCsv(data.reward_ratio_primary.toFixed(18)),
  escapeCsv(data.reward_ratio_secondary.toFixed(18)),
]);

// Combine headers and rows into CSV content
const csvContent = [
  headers.join(','),
  ...rows.map((row) => row.join(',')),
].join('\n');

// Write CSV file
try {
  fs.writeFileSync('removed_validators_summary.csv', csvContent, 'utf8');
  console.log('Successfully wrote removed_validators_summary.csv');
} catch (err) {
  console.error(`Error writing removed_validators_summary.csv: ${err.message}`);
  process.exit(1);
}
