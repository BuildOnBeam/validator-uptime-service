import dotenv from 'dotenv';
import fs from 'fs';
import { request, gql } from 'graphql-request';
import { Client } from 'pg';
import bs58 from 'bs58';
import ethers from 'ethers';

dotenv.config();

// Epoch constants
const EPOCH_DURATION = 2629746; // 1 month
const CURRENT_EPOCH = 665;
const EPOCH_START_TIMESTAMP = 1748725092; // Saturday, May 31, 2025 8:58:12 PM
const UPTIME_REWARDS_THRESHOLD_PERCENTAGE = 80;

// Reward pool constants
const TOTAL_REWARD_POOL = 16_000_000; // 16,000,000 ATH tokens
const PRIMARY_REWARD_POOL = TOTAL_REWARD_POOL * 0.2; // 3.2 million tokens
const SECONDARY_REWARD_POOL = TOTAL_REWARD_POOL * 0.8; // 12.8 million tokens

const SUBGRAPH_ENDPOINT = process.env.SUBGRAPH_URL;
const provider = new ethers.providers.JsonRpcProvider(process.env.RPC_URL);
const DB_CONFIG = {
  connectionString: process.env.DATABASE_URL,
};

const EXCLUDED_VALIDATION_IDS = [
  '22vKe8ZudEmtaciy5Kvs3k16sojaFKGCtrYGnNyUoqbcXmYXcd',
  'bnRZKQw6hsKpxJWgczPUanHQL3tVZHTcjgUbcqyuZDY26pCji',
  'L6Rxjo13a9GDPx1q1WtBipiofgqK986qRWGFgMmSksdpYXyE9',
  '3uG4UdQ9i1QVZ594JFF8F4qZZCQy2SSTLrbK6dZwZfkCB1VdB',
  '2YWWBy48XjqL33cMjTQ27g2kTvJpoqmWhJW1zpMxErAGnpNnyM',
  'djwQLt1M2x9Z5zaCNc1hRKPjkxS7oDEcr1UcJ1kz2Ft8AndSA',
  'JX4nGG7PkyztMMaFvRqNPSGNZXrpH5v2d1Ls84PheW2Hw76zS',
  'o3SPi9uzc8wYYG2tTkSTJfGSjmD5GhahkTwbD66GghHgVyDks',
  'fUjZnZA6mSCuERfCAdVR3XFat9gdp7UxbG8fDwC4kdCT9WxcH',
  '2kcyoN84fnyddUKXvvM1fLHemcGiTiL6E5nSUfAvGNJBoKpGxb',
  '25gz43vNQWzPawgHdZ4FrixkAGd6aW2ePASGMUKwsznD8RxmAz',
  'cy41KLdBfuZ4zJZQKGESQjHBZThJjWXWejmnFUGVrywLXcwTe',
  'A2oQic3Tb1xG5ZArpDJSeXGLL12TANkjZP1ugnupbg1fHN6gj',
];

const VALIDATIONS_QUERY = gql`
  query validatorRegistrations($ids: [Bytes!]) {
    validations(first: 1000, where: { id_in: $ids }) {
      id
      weight
      owner
      nodeID
      delegationFeeBips
      status
      tokenIDs
      startedAt
      endTime
      initiateRegistrationTx
    }
  }
`;

const DELEGATIONS_QUERY = gql`
  query delegations($validationID: String!) {
    delegations(first: 1000, where: { validationID: $validationID }) {
      id
      weight
      owner
      status
      tokenIDs
      startedAt
      endTime
      lastRewardedEpoch
    }
  }
`;

const iface = new ethers.utils.Interface([
  {
    name: 'initiateValidatorRegistration',
    type: 'function',
    stateMutability: 'payable',
    inputs: [
      { name: 'nodeID', type: 'bytes' },
      { name: 'blsPublicKey', type: 'bytes' },
      { name: 'registrationExpiry', type: 'uint64' },
      {
        name: 'remainingBalanceOwner',
        type: 'tuple',
        components: [
          { name: 'threshold', type: 'uint32' },
          { name: 'addresses', type: 'address[]' },
        ],
      },
      {
        name: 'disableOwner',
        type: 'tuple',
        components: [
          { name: 'threshold', type: 'uint32' },
          { name: 'addresses', type: 'address[]' },
        ],
      },
      { name: 'delegationFeeBips', type: 'uint16' },
      { name: 'minStakeDuration', type: 'uint64' },
      { name: 'tokenIDs', type: 'uint256[]' },
    ],
    outputs: [{ name: '', type: 'bytes32' }],
  },
]);

function validationIDToHex(cb58) {
  try {
    const decoded = bs58.decode(cb58);
    const payload = decoded.subarray(0, decoded.length - 4);
    return '0x' + Buffer.from(payload).toString('hex');
  } catch {
    return null;
  }
}

(async () => {
  const client = new Client(DB_CONFIG);
  await client.connect();

  const { rows } = await client.query(
    `SELECT validation_id, uptime_seconds
    FROM uptime_proofs
    WHERE updated_at >= to_timestamp($1)`,
    [EPOCH_START_TIMESTAMP]
  );

  const hexMap = new Map();
  for (const row of rows) {
    if (EXCLUDED_VALIDATION_IDS.includes(row.validation_id)) continue;
    const hex = validationIDToHex(row.validation_id);
    if (hex)
      hexMap.set(hex, {
        base58: row.validation_id,
        uptimeSeconds: parseInt(row.uptime_seconds, 10),
      });
  }
  await client.end();
  console.log(
    `📦 Loaded ${hexMap.size} validation IDs from DB (excluded ${EXCLUDED_VALIDATION_IDS.length})`
  );

  const hexIds = Array.from(hexMap.keys());
  const data = await request(SUBGRAPH_ENDPOINT, VALIDATIONS_QUERY, {
    ids: hexIds,
  });

  const all = [],
    removedOnly = [];
  let totalPrimary = 0,
    totalSecondary = 0;
  let activePrimary = 0,
    removedPrimary = 0;
  let activeSecondary = 0,
    removedSecondary = 0;
  let totalBeamDelegated = 0,
    totalNodesDelegated = 0;
  let totalBeamSelfStakedActive = 0,
    totalNodesSelfStakedActive = 0;
  let totalPrimaryRewardWeightSum = 0,
    totalSecondaryRewardWeightSum = 0;
  let totalCommissionRewardsPrimary = 0,
    totalCommissionRewardsSecondary = 0;
  const validatorStatuses = new Set();

  let index = 0;
  for (const v of data.validations) {
    index++;
    const meta = hexMap.get(v.id);
    if (!meta) continue;
    const validationId = meta.base58;
    const hexValidationId = v.id; // Hex validation ID
    const uptime = meta.uptimeSeconds;
    const feeBips = parseInt(v.delegationFeeBips);
    const start = parseInt(v.startedAt || '0', 10);
    const end = v.endTime ? parseInt(v.endTime) : null;

    validatorStatuses.add(v.status);

    console.log(
      `\n🔧 Processing #${index} - validationId ${validationId} (hex: ${hexValidationId})`
    );
    console.log(`  → Status: ${v.status}`);
    console.log(`  → Owner: ${v.owner}`);
    console.log(`  → Weight (subgraph): ${v.weight}`);
    console.log(`  → Delegation fee: ${(feeBips / 100).toFixed(2)}%`);

    let beam = 0,
      nodes = [];
    if (v.initiateRegistrationTx) {
      try {
        const tx = await provider.getTransaction(v.initiateRegistrationTx);
        const decoded = iface.decodeFunctionData(
          'initiateValidatorRegistration',
          tx.data
        );
        beam = parseFloat(ethers.utils.formatEther(tx.value));
        nodes = decoded.tokenIDs.map((id) => id.toString());
        console.log(`   ↪ Inspecting tx: ${v.initiateRegistrationTx}`);
        console.log(`     → Staked $BEAM: ${beam}`);
        console.log(`     → Node NFTs: ${nodes.length} (${nodes.join(',')})`);
        if (beam > 1000000) {
          console.warn(`⚠️  Large $BEAM stake detected: ${beam}`);
        }
        if (nodes.length > 1000) {
          console.warn(`⚠️  Large node count detected: ${nodes.length}`);
        }
      } catch (e) {
        console.warn(
          `⚠️  Failed to decode tx ${v.initiateRegistrationTx}: ${e.message}`
        );
      }
    }

    // Accumulate self-stakes for Active validators
    if (v.status === 'Active') {
      totalBeamSelfStakedActive += beam;
      totalNodesSelfStakedActive += nodes.length;
    }

    const duration = Math.max(
      0,
      (end ?? EPOCH_START_TIMESTAMP + EPOCH_DURATION) -
        Math.max(start, EPOCH_START_TIMESTAMP)
    );
    const effective = Math.min(duration, uptime);
    const ratio = Math.min(1, effective / EPOCH_DURATION);
    const finalRatio =
      (effective * 100) / EPOCH_DURATION >= UPTIME_REWARDS_THRESHOLD_PERCENTAGE
        ? 1
        : ratio;
    let selfBeamWeight = beam * finalRatio;
    let selfNodeWeight = nodes.length * 1e6 * finalRatio; // Scale NFT weight by 1e6

    if (ratio === 0) {
      console.warn(`⚠️  Validator ${validationId} has zero effective ratio`);
    }

    const dData = await request(SUBGRAPH_ENDPOINT, DELEGATIONS_QUERY, {
      validationID: v.id,
    });
    let commissionRewardsPrimary = 0,
      commissionRewardsSecondary = 0;
    let feeRatioPrimary = 0,
      feeRatioSecondary = 0;
    const delegators = [];

    const filteredDelegations = dData.delegations.filter((d) => {
      const start = Number(d.startedAt ?? 0);
      const end = d.endTime ? Number(d.endTime) : null;
      const delegationStart = Math.max(start, EPOCH_START_TIMESTAMP);
      const delegationEnd = end ?? EPOCH_START_TIMESTAMP + EPOCH_DURATION;
      const overlaps =
        delegationEnd > EPOCH_START_TIMESTAMP &&
        delegationStart < EPOCH_START_TIMESTAMP + EPOCH_DURATION;

      if (!overlaps) return false;

      // extra guard only for active validators
      if (v.status === 'Active') {
        const lastEpoch = Number(d.lastRewardedEpoch ?? 0);
        return lastEpoch === 0 || lastEpoch < CURRENT_EPOCH;
      }
      return true; // removed validator – always include if overlapping
    });

    for (const d of filteredDelegations) {
      const dStart = parseInt(d.startedAt || '0');
      const dEnd = d.endTime ? parseInt(d.endTime) : null;
      const dDur = Math.max(
        0,
        (dEnd ?? EPOCH_START_TIMESTAMP + EPOCH_DURATION) -
          Math.max(dStart, EPOCH_START_TIMESTAMP)
      );
      const dEff = Math.min(dDur, uptime);
      const dRatio = Math.min(1, dEff / EPOCH_DURATION);
      const dFinalRatio =
        (dEff * 100) / EPOCH_DURATION >= UPTIME_REWARDS_THRESHOLD_PERCENTAGE
          ? 1
          : dRatio;

      const isSecondary = Array.isArray(d.tokenIDs) && d.tokenIDs.length > 0;
      if (isSecondary) {
        totalNodesDelegated += d.tokenIDs.length;
      } else {
        totalBeamDelegated += parseFloat(d.weight);
      }

      const rewardWeight = isSecondary
        ? d.tokenIDs.length * 1e6 * dFinalRatio
        : parseFloat(d.weight) * dFinalRatio;

      if (isSecondary && d.tokenIDs.length > 1000) {
        console.warn(
          `⚠️  Large delegator node count detected: ${d.tokenIDs.length} for ${d.id}`
        );
      }
      if (!isSecondary && parseFloat(d.weight) > 1000000) {
        console.warn(
          `⚠️  Large delegator weight detected: ${d.weight} for ${d.id}`
        );
      }

      if (rewardWeight < 0) {
        console.warn(
          `⚠️  Negative rewardWeight for delegator ${d.id}: ${rewardWeight}`
        );
      }

      if (isSecondary) {
        totalSecondaryRewardWeightSum += rewardWeight;
        if (v.status === 'Active') {
          activeSecondary += rewardWeight;
        } else if (v.status === 'Removed') {
          removedSecondary += rewardWeight;
        }
        totalSecondary += rewardWeight;
      } else {
        totalPrimaryRewardWeightSum += rewardWeight;
        if (v.status === 'Active') {
          activePrimary += rewardWeight;
        } else if (v.status === 'Removed') {
          removedPrimary += rewardWeight;
        }
        totalPrimary += rewardWeight;
      }

      delegators.push({
        id: d.id,
        status: d.status,
        startTime: dStart,
        endTime: dEnd,
        owner: d.owner,
        weight: isSecondary ? d.tokenIDs.length : parseFloat(d.weight),
        rewardWeight: rewardWeight,
        nodeTokenCount: isSecondary ? d.tokenIDs.length : 0,
        type: isSecondary ? 'secondary' : 'primary',
        rewardRatio: 0,
        tokenRewards: 0,
        feeTokens: 0,
      });
    }

    // Assign self-stake weights by status
    if (v.status === 'Active') {
      activePrimary += selfBeamWeight;
      activeSecondary += selfNodeWeight;
    } else if (v.status === 'Removed') {
      removedPrimary += selfBeamWeight;
      removedSecondary += selfNodeWeight;
    }
    totalPrimary += selfBeamWeight;
    totalSecondary += selfNodeWeight;
    totalPrimaryRewardWeightSum += selfBeamWeight;
    totalSecondaryRewardWeightSum += selfNodeWeight;

    if (selfBeamWeight < 0) {
      console.warn(
        `⚠️  Negative selfBeamWeight for validator ${validationId}: ${selfBeamWeight}`
      );
    }
    if (selfNodeWeight < 0) {
      console.warn(
        `⚠️  Negative selfNodeWeight for validator ${validationId}: ${selfNodeWeight}`
      );
    }

    console.log(`  → Uptime: ${uptime}s`);
    console.log(`  → Effective ratio: ${ratio.toFixed(6)}`);
    console.log(
      `  → Validator self-stake weight: ${(
        selfBeamWeight + selfNodeWeight
      ).toFixed(6)}`
    );
    console.log(
      `  → Commission fees (primary): ${commissionRewardsPrimary.toFixed(
        6
      )} tokens`
    );
    console.log(
      `  → Commission fees (secondary): ${commissionRewardsSecondary.toFixed(
        6
      )} tokens`
    );
    console.log(`  → Delegators: ${delegators.length}`);

    const entry = {
      validationId,
      status: v.status,
      commissionRate: (feeBips / 100).toFixed(2) + '%',
      startTime: start,
      endTime: end,
      uptimeSeconds: uptime,
      type: 'validation',
      owner: v.owner,
      delegationCount: delegators.length,
      commissionRewardsPrimary: commissionRewardsPrimary,
      commissionRewardsSecondary: commissionRewardsSecondary,
      feeRatioPrimary: feeRatioPrimary,
      feeRatioSecondary: feeRatioSecondary,
      delegators: [
        {
          id: hexValidationId + '-self-beam',
          status: v.status,
          startTime: start,
          endTime: end,
          owner: v.owner,
          weight: beam,
          rewardWeight: selfBeamWeight,
          nodeTokenCount: 0,
          type: 'primary',
          rewardRatio: 0,
          tokenRewards: 0,
          feeTokens: 0,
        },
        {
          id: hexValidationId + '-self-nodes',
          status: v.status,
          startTime: start,
          endTime: end,
          owner: v.owner,
          weight: nodes.length,
          rewardWeight: selfNodeWeight,
          nodeTokenCount: nodes.length,
          type: 'secondary',
          rewardRatio: 0,
          tokenRewards: 0,
          feeTokens: 0,
        },
        ...delegators,
      ],
    };

    all.push(entry);
    if (v.status === 'Removed') removedOnly.push(entry);
  }

  console.log(
    `\n📊 Validator Statuses Found: ${Array.from(validatorStatuses).join(', ')}`
  );
  console.log(`📊 Total Primary Reward Weight: ${totalPrimary.toFixed(6)}`);
  console.log(`📊 Total Secondary Reward Weight: ${totalSecondary.toFixed(6)}`);
  console.log(`📊 Active Primary Reward Weight: ${activePrimary.toFixed(6)}`);
  console.log(`📊 Removed Primary Reward Weight: ${removedPrimary.toFixed(6)}`);
  console.log(
    `📊 Active Secondary Reward Weight: ${activeSecondary.toFixed(6)}`
  );
  console.log(
    `📊 Removed Secondary Reward Weight: ${removedSecondary.toFixed(6)}`
  );
  console.log(
    `📊 Total $BEAM Delegated (Delegators Only): ${totalBeamDelegated.toFixed(
      6
    )}`
  );
  console.log(
    `📊 Total Node NFTs Delegated (Delegators Only): ${totalNodesDelegated}`
  );
  console.log(
    `📊 Total $BEAM Self-Staked (Active Validators): ${totalBeamSelfStakedActive.toFixed(
      6
    )}`
  );
  console.log(
    `📊 Total Node NFTs Self-Staked (Active Validators): ${totalNodesSelfStakedActive}`
  );
  console.log(
    `📊 Sum of Primary rewardWeight: ${totalPrimaryRewardWeightSum.toFixed(
      6
    )} (should match ${totalPrimary.toFixed(6)})`
  );
  console.log(
    `📊 Sum of Secondary rewardWeight: ${totalSecondaryRewardWeightSum.toFixed(
      6
    )} (should match ${totalSecondary.toFixed(6)})`
  );

  // Calculate reward ratios and token rewards
  for (const validator of all) {
    let commissionRewardsPrimary = 0,
      commissionRewardsSecondary = 0;
    let feeRatioPrimary = 0,
      feeRatioSecondary = 0;
    const hexValidationId = validationIDToHex(validator.validationId);
    for (const delegator of validator.delegators) {
      const totalRewardPool =
        delegator.type === 'primary' ? totalPrimary : totalSecondary;
      const rewardPool =
        delegator.type === 'primary'
          ? PRIMARY_REWARD_POOL
          : SECONDARY_REWARD_POOL;
      delegator.rewardRatio =
        totalRewardPool > 0 ? delegator.rewardWeight / totalRewardPool : 0;
      let tokenRewards = delegator.rewardRatio * rewardPool;
      const isSelfStake =
        delegator.id === hexValidationId + '-self-beam' ||
        delegator.id === hexValidationId + '-self-nodes';
      const commissionRate =
        parseFloat(validator.commissionRate.replace('%', '')) / 100;
      const feeTokens = isSelfStake ? 0 : tokenRewards * commissionRate;
      delegator.feeTokens = feeTokens;
      delegator.tokenRewards = tokenRewards;
      if (isSelfStake) {
        console.log(
          `✅ Self-stake ${delegator.id} for validator ${validator.validationId} has feeTokens: ${feeTokens}`
        );
      }
      if (isSelfStake && feeTokens !== 0) {
        console.warn(
          `⚠️ Non-zero feeTokens for self-stake ${delegator.id}: ${feeTokens}`
        );
      }
      if (!isSelfStake) {
        if (delegator.type === 'primary') {
          commissionRewardsPrimary += feeTokens;
          feeRatioPrimary += delegator.rewardRatio * commissionRate;
        } else {
          commissionRewardsSecondary += feeTokens;
          feeRatioSecondary += delegator.rewardRatio * commissionRate;
        }
      }
    }
    validator.commissionRewardsPrimary = commissionRewardsPrimary;
    validator.commissionRewardsSecondary = commissionRewardsSecondary;
    validator.feeRatioPrimary = feeRatioPrimary;
    validator.feeRatioSecondary = feeRatioSecondary;
    totalCommissionRewardsPrimary += commissionRewardsPrimary;
    totalCommissionRewardsSecondary += commissionRewardsSecondary;

    console.log(
      `📊 Validator ${
        validator.validationId
      } Fee Ratio (Primary): ${feeRatioPrimary.toFixed(8)}`
    );
    console.log(
      `📊 Validator ${
        validator.validationId
      } Fee Ratio (Secondary): ${feeRatioSecondary.toFixed(8)}`
    );
  }

  // Do the same for removedOnly
  for (const validator of removedOnly) {
    let commissionRewardsPrimary = 0,
      commissionRewardsSecondary = 0;
    let feeRatioPrimary = 0,
      feeRatioSecondary = 0;
    const hexValidationId = validationIDToHex(validator.validationId);
    for (const delegator of validator.delegators) {
      const totalRewardPool =
        delegator.type === 'primary' ? totalPrimary : totalSecondary;
      const rewardPool =
        delegator.type === 'primary'
          ? PRIMARY_REWARD_POOL
          : SECONDARY_REWARD_POOL;
      delegator.rewardRatio =
        totalRewardPool > 0 ? delegator.rewardWeight / totalRewardPool : 0;
      let tokenRewards = delegator.rewardRatio * rewardPool;
      const isSelfStake =
        delegator.id === hexValidationId + '-self-beam' ||
        delegator.id === hexValidationId + '-self-nodes';
      const commissionRate =
        parseFloat(validator.commissionRate.replace('%', '')) / 100;
      const feeTokens = isSelfStake ? 0 : tokenRewards * commissionRate;
      delegator.feeTokens = feeTokens;
      delegator.tokenRewards = tokenRewards;
      if (isSelfStake) {
        console.log(
          `✅ Self-stake ${delegator.id} for validator ${validator.validationId} has feeTokens: ${feeTokens}`
        );
      }
      if (isSelfStake && feeTokens !== 0) {
        console.warn(
          `⚠️ Non-zero feeTokens for self-stake ${delegator.id}: ${feeTokens}`
        );
      }
      if (!isSelfStake) {
        if (delegator.type === 'primary') {
          commissionRewardsPrimary += feeTokens;
          feeRatioPrimary += delegator.rewardRatio * commissionRate;
        } else {
          commissionRewardsSecondary += feeTokens;
          feeRatioSecondary += delegator.rewardRatio * commissionRate;
        }
      }
    }
    validator.commissionRewardsPrimary = commissionRewardsPrimary;
    validator.commissionRewardsSecondary = commissionRewardsSecondary;
    validator.feeRatioPrimary = feeRatioPrimary;
    validator.feeRatioSecondary = feeRatioSecondary;
  }

  const summary = {
    totalPrimaryRewardWeight: totalPrimary.toFixed(8) + ' ($BEAM)',
    totalSecondaryRewardWeight: totalSecondary.toFixed(8) + ' (Node NFTs)',
    activePrimaryRewardWeight: activePrimary.toFixed(8),
    removedPrimaryRewardWeight: removedPrimary.toFixed(8),
    activePrimaryPercentage:
      ((activePrimary / totalPrimary) * 100).toFixed(8) + '%',
    removedPrimaryPercentage:
      ((removedPrimary / totalPrimary) * 100).toFixed(8) + '%',
    activeSecondaryRewardWeight: activeSecondary.toFixed(8),
    removedSecondaryRewardWeight: removedSecondary.toFixed(8),
    activeSecondaryPercentage:
      ((activeSecondary / totalSecondary) * 100).toFixed(8) + '%',
    removedSecondaryPercentage:
      ((removedSecondary / totalSecondary) * 100).toFixed(8) + '%',
    totalRewardPool: TOTAL_REWARD_POOL.toFixed(8) + ' tokens',
    primaryRewardPool: PRIMARY_REWARD_POOL.toFixed(8) + ' tokens',
    secondaryRewardPool: SECONDARY_REWARD_POOL.toFixed(8) + ' tokens',
    totalBeamDelegated: totalBeamDelegated.toFixed(8),
    totalNodesDelegated: totalNodesDelegated,
    totalBeamSelfStakedActive: totalBeamSelfStakedActive.toFixed(8),
    totalNodesSelfStakedActive: totalNodesSelfStakedActive,
    totalCommissionRewardsPrimary: totalCommissionRewardsPrimary.toFixed(8),
    totalCommissionRewardsSecondary: totalCommissionRewardsSecondary.toFixed(8),
  };

  // Write updated JSON outputs
  fs.writeFileSync(
    'epoch_rewards_snapshot_epoch3.json',
    JSON.stringify({ summary, validators: all }, null, 2)
  );
  fs.writeFileSync(
    'removed_validators_only_epoch3.json',
    JSON.stringify({ summary, validators: removedOnly }, null, 2)
  );

  console.log(
    `\n✅ Done. ${all.length} validators written. ${removedOnly.length} removed.`
  );
  console.log(
    `📊 Total Commission Rewards (Primary): ${totalCommissionRewardsPrimary.toFixed(
      2
    )} tokens`
  );
  console.log(
    `📊 Total Commission Rewards (Secondary): ${totalCommissionRewardsSecondary.toFixed(
      2
    )} tokens`
  );

  // Post-summary check: Sum tokenRewards and verify against reward pools
  let totalPrimaryTokenRewards = 0;
  let totalSecondaryTokenRewards = 0;

  for (const validator of all) {
    for (const delegator of validator.delegators) {
      if (delegator.type === 'primary') {
        totalPrimaryTokenRewards += delegator.tokenRewards;
      } else {
        totalSecondaryTokenRewards += delegator.tokenRewards;
      }
    }
  }

  console.log('\n🧮 Post-Summary Reward Pool Check:');
  console.log(
    `  → Primary Reward Pool: ${PRIMARY_REWARD_POOL.toFixed(2)} tokens`
  );
  console.log(
    `  → Sum of Primary tokenRewards: ${totalPrimaryTokenRewards.toFixed(
      2
    )} tokens`
  );
  const primaryDiff = Math.abs(PRIMARY_REWARD_POOL - totalPrimaryTokenRewards);
  console.log(
    `  → Difference: ${primaryDiff.toFixed(2)} tokens (${
      primaryDiff < 0.01 ? '✓ Pass' : '⚠️ Fail'
    })`
  );

  console.log(
    `  → Secondary Reward Pool: ${SECONDARY_REWARD_POOL.toFixed(2)} tokens`
  );
  console.log(
    `  → Sum of Secondary tokenRewards: ${totalSecondaryTokenRewards.toFixed(
      2
    )} tokens`
  );
  const secondaryDiff = Math.abs(
    SECONDARY_REWARD_POOL - totalSecondaryTokenRewards
  );
  console.log(
    `  → Difference: ${secondaryDiff.toFixed(2)} tokens (${
      secondaryDiff < 0.01 ? '✓ Pass' : '⚠️ Fail'
    })`
  );

  const totalCheck = totalPrimaryTokenRewards + totalSecondaryTokenRewards;
  const totalDiff = Math.abs(TOTAL_REWARD_POOL - totalCheck);
  console.log(`  → Total Reward Pool: ${TOTAL_REWARD_POOL.toFixed(2)} tokens`);
  console.log(`  → Sum of All tokenRewards: ${totalCheck.toFixed(2)} tokens`);
  console.log(
    `  → Total Difference: ${totalDiff.toFixed(2)} tokens (${
      totalDiff < 0.01 ? '✓ Pass' : '⚠️ Fail'
    })`
  );
})();
