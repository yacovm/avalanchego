# P-Chain Validator Management

*A reference for engineers working on the Avalanche P-chain in `avalanchego`.*

> **Abstract.** The Avalanche P-chain is the source of truth for who validates what. Over four network upgrades — Apricot, Banff, Durango, and Etna — its model for managing validators has evolved from a single, time-boxed staker on the primary network into a heterogeneous registry that simultaneously serves the primary network, two flavors of legacy subnet, and the L1 model introduced by ACP-77. Each generation has been added without removing the previous one. The result is a system that is simple in any one slice but rich in the union, and where small details — a zero in one field versus another, the order in which two block-level hooks run, or which database a row currently lives in — control whether a validator is producing blocks, gathering dust, or has been deleted forever. This document gives a top-down account of that system. It assumes familiarity with Avalanche concepts at the level of the public docs and works downward toward implementation specifics.

---

## 1. Introduction

The P-chain has three jobs related to validators. First, it is a **registry**: it records who is allowed to validate which chain, with what stake or weight, on behalf of which node. Second, it is the **activation engine**: it gates registered validators by start times, end times, balances, and signed authorizations from external parties so that the registered set and the *currently active* set diverge as a function of time. Third, it is a **signer-set oracle**: every other chain in the Avalanche ecosystem reads the P-chain to know whom to sample for consensus and whose BLS signatures to demand for Warp messages. All three jobs operate against the same underlying state, but each of them sees a slightly different projection of it.

These three jobs sound simple, but the complexity comes from the fact that the rules for *how a validator gets registered, activated, and exposed* differ for every category of chain — and have differed at every network upgrade. A primary network validator obtained through `AddValidatorTx` in 2020 is, today, kept on disk by the same code path as an L1 validator created by an ACP-77 Warp message in 2024. They share the validator-set exposure layer at the top, share the block-level scheduler in the middle, and diverge wildly in the executor logic and the state schema below. To work on this code productively, you need to know which slice you are in at any given moment.

This document is structured top-down on purpose. **Section 2** describes what validator management does in the abstract — the lifecycle and the cycle of authority — without naming any transaction. **Section 3** introduces the era timeline, the transaction types each era added, and the on-disk data structures and database layout that they all share. **Section 4** walks through the lifecycle of every kind of validator the system currently supports. **Section 5** descends into transaction execution and the block-level hooks that make all of this happen. **Section 6** collects the remaining material that does not fit cleanly into any of the above: validator manager contracts, mainnet specifics, common pitfalls, file maps, and a suggested reading order.

The intended reader is an avalanchego contributor implementing or modifying P-chain features. File and line references are concrete enough to navigate the code while reading, but liberally use approximations (`~ line N`) for places where exact line numbers are likely to drift; symbol names are stable.

---

## 2. High-Level Overview and Lifecycle

A validator on the P-chain has, at any moment, exactly one of four logical states:

- **Pending.** Registered but not yet contributing. The chain knows about it; consensus and Warp do not.
- **Active.** Producing blocks, voting in consensus, signing Warp messages on at least one chain.
- **Inactive.** Registered but suspended. Held in P-chain state, but excluded from sampling and signature aggregation. Reactivation is possible without re-registering.
- **Removed.** No longer in P-chain state. Recreating it requires going through registration again.

Every validator transitions through some prefix of `pending → active → inactive → removed`, but not every category visits every state. Primary network validators, for instance, never become inactive; they expire from active to removed at a fixed end time. L1 validators, by contrast, have no fixed end time at all and oscillate between active and inactive depending on whether their continuous-fee balance is funded.

The transitions themselves are produced by three mechanisms:

1. **A transaction issued by an authorized party.** Registers, modifies, or removes a validator. The notion of "authorized" is itself a function of the validator's category and lifecycle stage.
2. **The advancement of chain time.** Pending validators with start times in the past become active; active validators with end times in the past expire. This is mechanical; no tx is required to trigger it.
3. **Per-block bookkeeping.** Each P-chain block charges a continuous fee against L1 validators' balances, and any validator whose balance can no longer cover the next second is moved from active to inactive automatically.

Cutting across all of this is what we will call the **cycle of authority**: at any point in time, a change to a validator's record requires authorization from *somebody*, and the identity of that somebody changes over the validator's life. Initially, registration is authorized by either the network's permissionless staking rules (anyone with the minimum stake) or by the subnet's owner key. After a subnet has been "converted" to an L1 — the central event of ACP-77 — authority for any change to that subnet's validators moves off-chain entirely, into a contract running *on the L1 itself*. From then on, the P-chain accepts validator-changing transactions for that subnet only when they carry a Warp message signed by the L1's existing validators. This bootstraps a feedback loop: the active set authorizes its own evolution. The loop's only fragile assumption is that the active set is non-empty, which the code enforces by refusing to remove the last registered validator from a converted subnet.

The exposure side of the system has its own simple shape. After a transaction commits a change to validator state, the new state is reflected in three places: in an in-memory map used by the local node's snowman engines for sampling; in a per-height diff store on disk, so that historical lookups are possible; and in a canonicalized "Warp set" structure, which is what aggregators and verifiers use to interpret BLS signatures. Inactive and removed validators contribute zero weight to the Warp set, regardless of their on-disk presence.

That, in essence, is the system. The rest of this document is the implementation.

---

## 3. Eras, Transactions, and State

### 3.1 Network upgrade timeline

Validator behavior on the P-chain is gated by four upgrade points. Each one is a hard fork at a network-specific timestamp, configured per network in `upgrade/upgrade.go`:

| Upgrade | Validator-management additions |
|---|---|
| **Apricot** | Original P-chain. Primary network validators registered through proposal blocks; permissioned subnets and their validators introduced. |
| **Banff** | New block types (`BanffProposalBlock`, `BanffStandardBlock`). Permissionless subnet validators (post-`TransformSubnetTx`) become possible via a new tx family. Primary-network registration moves into standard blocks; reward processing stays in proposal blocks. |
| **Durango** | Stakers entering the system go directly into the *current* set with `StartTime = chainTime`. The pending-staker queue continues to exist for stakers admitted before Durango, but new admissions skip it. |
| **Etna (ACP-77)** | The L1 model. New `L1Validator` data structure, five new transaction types, a continuous-fee charge applied per P-chain block, and a permanent immutability boundary for any subnet that opts in. |

Each section below introduces a new tx, that tx applies only after its activation point, and earlier behavior continues to apply for entities created before that point. There is no clean cutover; new code paths layer on top of old ones, and tests routinely exercise both.

### 3.2 Transaction taxonomy

There are roughly twenty validator-relevant transaction types, but they fall into a small number of categories. We group them by *what they do*, not by *which era introduced them*; the era column is a footnote on the row.

| Category | Transactions | Era | Authorization |
|---|---|---|---|
| Subnet creation and ownership | `CreateSubnetTx`, `TransferSubnetOwnershipTx` | Apricot, Banff | Subnet owner key |
| Subnet transformation (legacy permissionless) | `TransformSubnetTx` | Apricot | Subnet owner key, one-shot |
| Chain creation | `CreateChainTx` | Apricot | Subnet owner key |
| Primary-network legacy stakers | `AddValidatorTx`, `AddDelegatorTx` | Apricot only | Permissionless (stake + duration) |
| Primary-network and permissionless-subnet stakers | `AddPermissionlessValidatorTx`, `AddPermissionlessDelegatorTx` | Banff+ | Permissionless |
| Permissioned subnet stakers | `AddSubnetValidatorTx`, `RemoveSubnetValidatorTx` | Apricot, Banff | Subnet owner key |
| Reward distribution | `RewardValidatorTx` | Apricot+ (auto-included by builder) | None — emitted by the chain itself |
| Subnet → L1 conversion | `ConvertSubnetToL1Tx` | Etna+ | Subnet owner key, final use |
| L1 validator registration | `RegisterL1ValidatorTx` | Etna+ | Warp message from the L1's validator manager |
| L1 validator weight changes (incl. removal) | `SetL1ValidatorWeightTx` | Etna+ | Warp message signed by the L1's current validators |
| L1 validator funding | `IncreaseL1ValidatorBalanceTx` | Etna+ | None — anyone can fund anyone |
| L1 validator manual deactivation | `DisableL1ValidatorTx` | Etna+ | Validator's `DeactivationOwner` signature |

Two structural patterns are worth noticing.

The first pattern is **the contraction of authority over time**. Apricot-era transactions for permissioned subnets are authorized by an off-chain key; Etna-era transactions for the same subnet — *after conversion* — are authorized only by Warp messages from the chain itself. Conversion is the moment at which the subnet owner key is permanently retired in favor of the L1 manager contract.

The second pattern is **the absence of a delete operation for high-level entities**. There is `RemoveSubnetValidatorTx` for removing a single legacy validator, and `SetL1ValidatorWeightTx(weight=0)` for removing a single L1 validator, but there is no `DeleteSubnetTx`, no `DeleteChainTx`, and no `DeconvertSubnetTx`. Subnets, chains, and L1 conversions are permanent commitments by design.

### 3.3 Data structures

Three structures carry essentially all of the per-validator state.

**`Staker`** — defined in `vms/platformvm/state/staker.go`. Used for primary-network validators and delegators, for legacy permissioned subnet validators, and for legacy permissionless subnet validators of subnets that have been transformed but not converted.

```go
type Staker struct {
    TxID            ids.ID
    NodeID          ids.NodeID
    PublicKey       *bls.PublicKey   // nil for delegators
    SubnetID        ids.ID
    Weight          uint64
    StartTime       time.Time
    EndTime         time.Time
    PotentialReward uint64
    NextTime        time.Time        // == StartTime if pending; == EndTime if current
    Priority        Priority         // tie-breaker for ordered iteration
}
```

The `NextTime` field is what makes a staker schedulable. Pending stakers store their start time in `NextTime`; current stakers store their end time. Iteration in chain-time order then walks "the next interesting moment" without distinguishing between activations and expirations until the moment itself arrives.

**`L1Validator`** — defined in `vms/platformvm/state/l1_validator.go`. Used for every validator of a subnet that has been converted to an L1.

```go
type L1Validator struct {
    ValidationID          ids.ID
    SubnetID              ids.ID
    NodeID                ids.NodeID
    PublicKey             []byte   // BLS, set by the L1 itself
    RemainingBalanceOwner []byte   // serialized PChainOwner
    DeactivationOwner     []byte
    StartTime             uint64
    Weight                uint64   // 0 means deleted
    MinNonce              uint64
    EndAccumulatedFee     uint64   // 0 means inactive (balance exhausted)
}
```

L1 validators have no end time. Their lifecycle is governed by the two zero-encodings on the right-hand fields:

```go
func (v L1Validator) isDeleted() bool { return v.Weight == 0 }
func (v L1Validator) IsActive() bool  { return v.Weight != 0 && v.EndAccumulatedFee != 0 }
```

The cross-product gives:

| `Weight` | `EndAccumulatedFee` | Meaning |
|---|---|---|
| `> 0` | `> 0` | Active |
| `> 0` | `0` | Inactive (registered, suspended) |
| `0` | (anything) | Deleted (removed from disk on the next diff flush) |

These two predicates do an enormous amount of work in the codebase. Every visibility decision, every storage routing decision, and every authorization fast-path consults one or both.

**`SubnetToL1Conversion`** — defined in `vms/platformvm/state/state.go`. One row per subnet that has been converted.

```go
type SubnetToL1Conversion struct {
    ConversionID ids.ID  // hash of the conversion data
    ChainID      ids.ID  // the L1's manager chain
    Addr         []byte  // the L1's manager contract address
}
```

Once this record is written, the subnet is permanently an L1. Its mere presence is the gate for `errIsImmutable` rejections of legacy subnet modifications, and its `ChainID` and `Addr` are what every Warp-authenticated transaction checks against the source of inbound Warp messages.

### 3.4 Storage layout

The state stack is layered. Above the on-disk database is a `Diff` that batches a block's mutations; above the diff are the in-memory caches and indices that the executors consult; the `validators.Manager` holds an even higher in-memory projection meant to be cheap to read from any chain on the local node.

For validators specifically, the on-disk schema is split as follows:

- **Stakers** live in a B-tree keyed by `(NextTime, SubnetID, NodeID, TxID)`. There are two such trees, one for pending and one for current. Construction goes through `state.NewPendingStaker` / `state.NewCurrentStaker`, lookup through `state.GetPendingValidator` / `state.GetCurrentValidator`.
- **L1 validators** live in two separate KV stores: `activeDB` for those with `IsActive() == true`, and `inactiveDB` for those with `IsActive() == false`. A third KV store, `subnetIDNodeIDDB`, indexes `(subnetID, nodeID) → validationID` to support efficient lookup by node ID. The diff-flush path is responsible for moving entries between the active and inactive DBs as their state changes, and for deleting all three rows when `isDeleted()` becomes true. No executor calls `Delete` directly; the storage layer interprets the in-memory `Weight == 0` flag at flush time.
- **Validator-set diffs** live in two more KV stores, both prefixed by subnet ID and height: a weight-diff store and a public-key-diff store. These are written every block in which a relevant change occurred and are the substrate for historical validator-set reconstruction.
- **Subnet metadata** — owner, transformation, conversion — lives in dedicated KV stores keyed by subnet ID.

The DB prefixes (e.g. `SubnetToL1ConversionPrefix = []byte("subnetToL1Conversion")`) are declared at the top of `vms/platformvm/state/state.go` and are part of the on-disk format; changing one is a hard-fork-grade change.

---

## 4. Validator Lifecycles

This section traces every validator category from creation to disappearance. The arrows in the diagrams are state transitions; the labels on the arrows describe what causes them.

### 4.1 Primary network validator (post-Durango)

```
                     AddPermissionlessValidatorTx
                                │
                                ▼
                   (StandardBlock executes the tx)
                                │
                                ▼
              ┌────────────────────────────────────┐
              │   ACTIVE in primary network set    │
              │   PotentialReward computed         │
              └────────────────────────────────────┘
                                │
                                │  chain time advances to == EndTime
                                ▼
            ┌──────────────────────────────────────┐
            │  BanffProposalBlock issues           │
            │  RewardValidatorTx                   │
            │   ├── Commit: stake returned + reward│
            │   └── Abort:  stake returned, no rew │
            └──────────────────────────────────────┘
                                │
                                ▼
                      (validator removed)
```

Primary-network validators do not become inactive. They are admitted directly to the current set, sit there until their stated end time, and at that moment the block builder issues a `RewardValidatorTx` proposal block. The proposal block's two children represent the two possible decisions about the reward; the staker is removed from the current set in either case.

The pre-Durango variant is the same except for one extra hop: the staker is admitted to the *pending* set with `StartTime` in the future, and `AdvanceTimeTo` promotes it to current at activation time. Stakers admitted before the Durango fork still follow this path even after the fork.

The pre-Banff variant uses `AddValidatorTx` instead of `AddPermissionlessValidatorTx`, and the admission happens through an `ApricotProposalBlock`'s commit child rather than through a standard block.

### 4.2 Legacy permissioned subnet validator

```
       Subnet owner issues AddSubnetValidatorTx
                          │
                          ▼
              (verifyPoASubnetAuthorization)
                          │
                          ▼
       ┌────────────────────────────────────┐
       │  PENDING (or CURRENT under Durango)│
       └────────────────────────────────────┘
                          │
                          │ activation time (if pending)
                          ▼
       ┌────────────────────────────────────┐
       │  ACTIVE in subnet validator set    │
       │  No reward computation             │
       └────────────────────────────────────┘
                          │
                          │ either: chain time >= EndTime
                          │ or:     subnet owner issues
                          │         RemoveSubnetValidatorTx
                          ▼
                   (validator removed)
```

Permissioned subnet validators have no economics — there is no stake, no reward, and removal happens silently when their end time is reached. `AdvanceTimeTo` removes them in-line during block verification; no proposal block is needed. They can also be removed early by the subnet owner. Both the addition and the removal must be authorized by the subnet's owner key.

If the subnet is transformed via `TransformSubnetTx`, future stakers must be added through `AddPermissionlessValidatorTx` (subnet variant) and follow the post-Banff flow described in §4.1, except that the BLS public key is optional and rewards are paid out of the subnet's transformed-stake supply rather than from the primary network's reward calculator.

### 4.3 L1 validator (post-Etna)

```
   Subnet conversion seeds initial L1 validators       (one-time)
   OR  L1 manager Warp-signs RegisterL1ValidatorTx     (recurring)
                              │
                              ▼
           ┌─────────────────────────────────────┐
           │ ACTIVE                              │
           │ Weight > 0, EndAccumulatedFee > 0   │
           └─────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
        │ continuous-fee      │ DisableL1Validator  │ SetL1ValidatorWeightTx
        │ depletes balance    │ (DeactivationOwner) │ (msg.Weight = 0,
        │                     │                     │  Warp-signed)
        ▼                     ▼                     ▼
  ┌──────────────┐      ┌──────────────┐      ┌──────────────┐
  │ INACTIVE     │      │ INACTIVE     │      │ DELETED      │
  │ Weight > 0   │ ◀──► │ Weight > 0   │      │ Weight = 0   │
  │ EAF = 0      │      │ EAF = 0      │      │              │
  └──────────────┘      └──────────────┘      └──────────────┘
        │                     │
        └─────────┬───────────┘
                  ▼
       IncreaseL1ValidatorBalanceTx (anyone)
                  │
                  ▼
         (back to ACTIVE if Capacity allows)
```

The lifecycle differs from the legacy stakers in three structural ways. First, there is no end time — an L1 validator stays registered until the L1's manager contract authorizes its removal via a Warp-signed `SetL1ValidatorWeightTx(weight=0)`. Second, "inactive" is an explicit on-disk state, not a transient one; an inactive L1 validator can sit in P-chain state indefinitely. Third, balance depletion is reversible: any account can call `IncreaseL1ValidatorBalanceTx` to top up an inactive validator and restore it to active, subject only to a global cap on the total number of active L1 validators (`ValidatorFeeConfig.Capacity`).

The "deleted" branch is the only one that loses information. It is also the only branch that is rate-limited in a special way: if the operation would empty the subnet's total weight, the executor rejects the transaction with `errRemovingLastValidator`. This guarantees that every converted subnet always has at least one registered validator, and therefore at least one possible signer for the *next* Warp-authorized change. Without this guard, an L1 could be permanently bricked by a single weight-zero message.

A note on funding semantics. `IncreaseL1ValidatorBalanceTx` does *not* keep track of "remaining balance" directly. It maintains `EndAccumulatedFee = (the value of accruedFees at which this validator will fall below zero)`. Topping up sets `EndAccumulatedFee = GetAccruedFees() + tx.Balance`; deactivation sets `EndAccumulatedFee = 0`; depletion is detected by the comparison `EndAccumulatedFee <= accruedFees + costOfNextSecond`. The displayed "balance" of the validator is computed at read time as `EndAccumulatedFee - accruedFees`.

### 4.4 Subnet → L1 conversion

```
       CreateSubnetTx                                 (Apricot+)
       AddSubnetValidatorTx, CreateChainTx, etc.      (legacy management)
                              │
                              ▼
       ConvertSubnetToL1Tx                            (Etna+)
       ── verifyPoASubnetAuthorization               (last use of owner key)
       ── creates one L1Validator per validator in tx
       ── writes SubnetToL1Conversion(ChainID, Addr)
                              │
                              ▼
       Subnet is now an L1.
       Legacy modification txs blocked by errIsImmutable.
       All future changes arrive as Warp messages from (ChainID, Addr).
```

The conversion does not migrate existing legacy stakers. If the subnet had `AddSubnetValidatorTx` validators before conversion, those `Staker` rows remain on disk after conversion, but they are inert: their authorization key has been retired and the validator-set exposure layer no longer surfaces them for converted subnets. The intended pattern is for the subnet operator to clean up legacy stakers before conversion, but the chain does not enforce this.

### 4.5 Mainnet primary network validators

The primary network is, structurally, the simplest case: it is a subnet that has *not* been transformed and has *not* been converted. Its validators are governed by the constants in `vms/platformvm/config/` (mainnet has a 2 ka.k AVAX minimum stake, a 14-day minimum duration, and a 365-day maximum duration), and they go through the lifecycle of §4.1. Primary-network validators automatically become validators of every L1 by virtue of their primary-network membership; this linkage is enforced at the validator-set exposure layer rather than in the staker table itself.

Mainnet does not differ from fuji or local networks in lifecycle — only in numerical parameters and in the activation timestamps for each upgrade. Anything that special-cases on `constants.MainnetID` is doing so for parameter choice, not for behavior choice.

---

## 5. Transaction Execution and Implementation Details

The previous sections described the system in terms of states and transitions. This section describes how those states and transitions are realized in code: the executor architecture that processes transactions, the block-level hooks that run between transactions, and the validator-set exposure layer that publishes the result.

### 5.1 Executor architecture

A transaction's effect on state is delegated, via the `txs.Visitor` interface, to one of four executor implementations selected by the surrounding block type:

- **`standardTxExecutor`** — `vms/platformvm/txs/executor/standard_tx_executor.go`. Runs inside `BanffStandardBlock` and post-Banff Apricot standard blocks. Most modern transactions live here, including all five Etna transactions and `AddPermissionless*Tx`.
- **`proposalTxExecutor`** — `vms/platformvm/txs/executor/proposal_tx_executor.go`. Runs inside `ApricotProposalBlock` and `BanffProposalBlock`. Today this is dominated by `RewardValidatorTx`; in Apricot it also handled `AddValidatorTx`, `AddDelegatorTx`, and `AddSubnetValidatorTx`.
- **`atomicTxExecutor`** — `vms/platformvm/txs/executor/atomic_tx_executor.go`. Runs inside Apricot atomic blocks. Returns errors for non-atomic transactions.
- **`warpVerifier`** — `vms/platformvm/txs/executor/warp_verifier.go`. A pre-execution pass that authenticates inbound Warp messages on transactions that carry them.

A transaction in flight is dispatched to exactly one of these executors based on the block type containing it; the dispatch uses Go's visitor pattern and a `tx.Visit(executor)` call.

The executors share two structural elements. They take a `state.Diff` in which to record their changes, and they use a `FlowChecker` to verify that any AVAX-denominated inputs and outputs balance after fees. Beyond that, each executor's per-tx method has a stereotypical shape: syntactic verification, authorization, semantic verification, state mutation.

### 5.2 Block-level hooks

Sandwiched around the executor invocations are several hooks that run per block. Their order is precise and load-bearing.

**`AdvanceTimeTo`** — `vms/platformvm/txs/executor/state_changes.go:90+`. Called by the block verifier *before* any tx in the block runs, with the new chain time as its argument. It performs four jobs in order:

1. Promote pending stakers whose `StartTime <= newChainTime` to current.
2. Remove current *permissioned* stakers whose `EndTime <= newChainTime`. Permissionless stakers are not removed here; they are removed by `RewardValidatorTx`.
3. Run `advanceValidatorFeeState`. For each elapsed second, accrue the continuous fee: `accruedFees += fee.State.CostOf(config, 1s)`, then `fee.State.AdvanceTime(config.Target, 1s)` updates the dynamic-fee `Excess`. Any L1 validator whose `EndAccumulatedFee <= accruedFees` is moved to inactive.
4. Persist the new chain timestamp.

The crucial implication: a transaction inside a `BanffStandardBlock` sees the chain time, the staker set, the fee state, and the L1 validator activity bits as they are *after* `AdvanceTimeTo` has run. Code that reads `e.state.GetTimestamp()` from inside a tx executor is reading the *post*-advancement value.

**`processStandardTxs`** — `vms/platformvm/block/executor/verifier.go:524+`. Iterates the block's transactions, dispatches each one to `standardTxExecutor`, and consumes block gas (Etna+).

**`deactivateLowBalanceL1Validators`** — `vms/platformvm/block/executor/verifier.go:673+`. Runs *after* the transactions are processed, in addition to its earlier invocation inside `advanceValidatorFeeState`. Catches validators whose balance has just been drained by a tx-induced fee change. The algorithm is essentially:

```text
threshold = accruedFees + costOfNextSecond
for each active L1Validator, in ascending EndAccumulatedFee order:
    if EndAccumulatedFee >= threshold:
        break
    setEndAccumulatedFee(validator, 0)
```

Iterating in ascending order is what makes the loop bounded: as soon as one validator can pay, all the rest can too.

The standard-block hook ordering is therefore:

```
   ┌──────────────────────────────────────────────────────┐
   │ BanffStandardBlock(t')                               │
   │                                                      │
   │  AdvanceTimeTo(t')                                   │
   │   ├── pending → current                              │
   │   ├── current (permissioned) → removed               │
   │   └── advanceValidatorFeeState                       │
   │        ├── accruedFees += per-second cost            │
   │        └── deactivate underfunded L1 validators ★   │
   │                                                      │
   │  processStandardTxs                                  │
   │    └── for each tx: standardTxExecutor.dispatch      │
   │                                                      │
   │  deactivateLowBalanceL1Validators ★                  │
   └──────────────────────────────────────────────────────┘

   ┌──────────────────────────────────────────────────────┐
   │ BanffProposalBlock(t')   (always t' == staker.EndTime)│
   │                                                       │
   │  AdvanceTimeTo(t')                                    │
   │  proposalTxExecutor.RewardValidatorTx                 │
   │    ├── Commit child:  stake returned + reward         │
   │    └── Abort  child:  stake returned, no reward       │
   └──────────────────────────────────────────────────────┘
```

The two `★` lines are the two L1-deactivation passes per standard block. A standard block must produce *some* state change; otherwise it is rejected with `ErrStandardBlockWithoutChanges` (`verifier.go:495`). This invariant is what prevents the network from issuing empty filler blocks.

The block builder (`vms/platformvm/block/builder/builder.go:198+`) clamps the next block's timestamp to `min(max(now, parent.Timestamp), nextStakerChangeTime)`, so any moment at which a staker enters or leaves the system is exposed as a block boundary. It then either issues a `BanffProposalBlock` with an auto-included `RewardValidatorTx` (if a permissionless staker's end time matches the chosen timestamp) or a `BanffStandardBlock` with the mempool's transactions.

### 5.3 Specific executor walk-throughs

The five Etna-era transactions are the most subtle of the bunch. Each is worth a brief tour.

**`ConvertSubnetToL1Tx`** (`standard_tx_executor.go:739-870`). After Etna gating, it calls `verifyPoASubnetAuthorization` — the last time the legacy subnet owner key is consulted for this subnet. It then iterates the validators in the conversion tx, computes a per-validator `validationID = tx.Subnet.Append(uint32(i))`, and writes one fresh `L1Validator` per slot via `state.PutL1Validator`. Finally, it writes the `SubnetToL1Conversion` row with the L1 manager's chain ID and contract address. Existing legacy `Staker` rows for this subnet are not migrated and not removed.

**`RegisterL1ValidatorTx`**. Verifies that the inbound Warp message originated from the converted subnet's manager (chain ID and source address must match the conversion record), parses the registration payload, and creates a new `L1Validator` with the parsed fields. The `validationID` here comes from the message itself, not from the surrounding tx.

**`SetL1ValidatorWeightTx`** (`standard_tx_executor.go:1030+`). The most lifecycle-rich of the five. It loads the existing `L1Validator` by `msg.ValidationID`, checks `msg.Nonce >= l1Validator.MinNonce` for replay protection, verifies the Warp source, and then branches on `msg.Weight`:

- If `msg.Weight != 0`, it updates `MinNonce` and `Weight` and writes back.
- If `msg.Weight == 0`, it first checks that this is not the last validator on the subnet (`errRemovingLastValidator`); then, if the validator was active, it refunds the remaining balance to `RemainingBalanceOwner` as a new UTXO; finally it sets `Weight = 0` and writes back, which the diff layer will interpret as a delete.

The deletion is implicit: there is no `state.DeleteL1Validator` call from the executor. The flush logic in `state.go`'s `writeL1Validators` consults `isDeleted()` and removes the row from whichever DB it was in along with the `subnetIDNodeIDDB` index entry.

**`IncreaseL1ValidatorBalanceTx`** (`standard_tx_executor.go:1176+`). Loads the validator and either credits its balance (if active) or reactivates and credits it (if inactive). The reactivation branch is what enforces the global cap:

```go
if l1Validator.EndAccumulatedFee == 0 {
    if gas.Gas(e.state.NumActiveL1Validators()) >= e.backend.Config.ValidatorFeeConfig.Capacity {
        return errMaxNumActiveValidators
    }
    l1Validator.EndAccumulatedFee = e.state.GetAccruedFees()
}
l1Validator.EndAccumulatedFee += tx.Balance
```

Setting `EndAccumulatedFee = GetAccruedFees()` first means the validator's *displayed* balance starts at zero before the increment.

**`DisableL1ValidatorTx`** (`standard_tx_executor.go:1253+`). Authorizes via the validator's `DeactivationOwner`, sets `EndAccumulatedFee = 0`, and refunds the remaining balance. The validator is now inactive and can be reactivated by anyone via `IncreaseL1ValidatorBalanceTx`. It is *not* deleted.

### 5.4 Validator-set exposure

After a transaction commits, its effects must reach three audiences: this node's snowman engines (for sampling), Warp aggregators and verifiers (for signature counting), and external RPC callers. All three read through the `validators.Manager` and `validators.State` interfaces in `snow/validators/`, which the P-chain VM implements via `vms/platformvm/validators/manager.go`.

The hot path is `GetValidatorSet(ctx, height, subnetID)`. On a cache miss, the P-chain implementation:

1. Builds the *current* validator set for the given subnet by calling `getCurrentValidatorSet`. For a non-converted subnet, this is the in-memory map that the VM has been mutating as transactions commit. For a converted subnet, this is instead a join of the `subnetIDNodeIDDB` index with the `L1Validator` rows, filtering for `IsActive() == true`.

2. Walks backwards from the current height down to `height + 1`, applying the inverse of each height's diffs via `state.ApplyValidatorWeightDiffs` and `state.ApplyValidatorPublicKeyDiffs`. Because the walk is backwards, a recorded `Decrease == true` is *added back* to the running total. This is the place where the direction of diffs most often confuses readers.

3. Caches the result by `(height, subnetID)` and returns it.

For Warp-specific use, `GetWarpValidatorSets(ctx, height)` is similar but returns a canonicalized `WarpSet` per subnet: validators are placed into a deterministic order (so signers and verifiers agree on indices), and validators with no BLS public key are filtered out via `FlattenValidatorSet` in `snow/validators/warp.go`. Inactive L1 validators contribute nothing to this output, regardless of their on-disk state, because their `effectivePublicKey()` returns an empty slice (`l1_validator.go:168-195`).

The diff machinery is small, but it is what makes the system work for cross-chain queries. Any chain that wants to verify a Warp signature signed against the P-chain validator set at some past height — say, last week's snapshot of an L1's signers — needs the diffs back to that height to be present and correct. They are written every block in which a validator's weight or public key changes, as part of the same diff that records the staker or L1 validator change itself.

---

## 6. Other Considerations

### 6.1 Validator manager contracts

The L1 validator manager contract is the single most important piece of this system that does not live in `avalanchego`. It is an application-defined contract running *on the L1 itself*, deployed at the address recorded in the subnet's `SubnetToL1Conversion`. It is responsible for deciding which validators the L1 admits, with what weights, when to remove them, and for emitting and consuming the Warp messages that synchronize this decision with the P-chain. ACP-77 is generic over the contract's environment, but in practice every existing manager runs on a `subnet-evm`-based chain and is written in Solidity; reference Proof-of-Authority and Proof-of-Stake implementations are maintained in the `ava-labs/icm-contracts` repository.

This subsection explains, end to end, how that off-chain authority is realized: where the contract runs, how it emits a Warp message that the P-chain accepts, how the P-chain emits Warp messages back, what each of the four message types is for, what relayers do in between, and what assumptions the protocol makes about the manager's identity.

#### 6.1.1 Where the contract runs and how it emits messages

The manager contract lives at a normal contract address on the L1. On a `subnet-evm` chain that means the EVM treats it as any other contract: external accounts call its methods, it reads and writes its own storage, and it can invoke precompiles. The piece that matters for validator management is the **Warp precompile**, deployed by `subnet-evm` at a well-known address (`0x02...05`). Calling its `sendWarpMessage(payload)` method records an *unsigned* `WarpMessage` in the precompile's logs whose source chain is the chain ID the contract is running on, whose source address is the contract's own address, and whose payload is the raw bytes the contract supplied.

The manager does not pick those payload bytes arbitrarily — to be acceptable on the P-chain, they must be a serialized `AddressedCall` (defined in `vms/platformvm/warp/payload/`) whose inner payload is one of the four message types in `vms/platformvm/warp/message/`:

| Message | Source | Purpose |
|---|---|---|
| `RegisterL1Validator` (`register_l1_validator.go`) | L1 → P-chain | Request to add a new validator. Carries `SubnetID`, `NodeID`, `BLSPublicKey`, `Weight`, `Expiry`, and two `PChainOwner` structures: `RemainingBalanceOwner` (refund recipient) and `DisableOwner` (deactivation authority). |
| `L1ValidatorWeight` (`l1_validator_weight.go`) | L1 → P-chain *or* P-chain → L1 | When sent by the L1, a command to change weight (or, with `Weight = 0`, to remove). When sent by the P-chain, a confirmation reporting the validator's current `(nonce, weight)`. |
| `L1ValidatorRegistration` (`l1_validator_registration.go`) | P-chain → L1 | Confirms `(ValidationID, Registered=true)` if the validator is currently in P-chain state, or `(ValidationID, Registered=false)` if it has been removed and can never come back. |
| `SubnetToL1Conversion` (`subnet_to_l1_conversion.go`) | P-chain → L1 | Reports the canonical `ConversionID` for a subnet, used by the manager during bootstrap to verify that it was deployed against the correct conversion. |

The README at `vms/platformvm/warp/message/README.md` summarizes this: every payload is an `AddressedCall` with an empty source address (the L1 → P-chain direction sets the source to the manager contract; the P-chain → L1 direction sets it to empty because the P-chain has no contract address).

`AddressedCall` itself is parsed in the executor like this (`standard_tx_executor.go:1077-1106`):

```text
warpMessage := warp.ParseMessage(tx.Message)             // outer signed envelope
addressedCall := payload.ParseAddressedCall(warpMessage.Payload)
msg := message.ParseRegisterL1Validator(addressedCall.Payload)   // (or weight, conversion, etc.)
```

So the data flow inside one inbound transaction is: a signed Warp envelope wraps an addressed call, which wraps an ACP-77 message. The signature on the envelope is what authorizes the change; the addressed call's `SourceChainID` and `SourceAddress` are what tie the envelope back to the manager contract; and the inner message is what the P-chain actually executes against state.

#### 6.1.2 The L1 → P-chain authorization model

When an external caller asks the manager to register a validator (typically by calling, in Solidity, an `initiateValidatorRegistration` method or similar), the manager contract:

1. Performs whatever business logic the L1's operator chose — Proof-of-Authority key checks, Proof-of-Stake bonding, capacity checks, etc.
2. Builds a `RegisterL1Validator` payload, wraps it in an `AddressedCall` (with the manager's own address as the source), and emits it through the Warp precompile.
3. Returns to the caller. The transaction commits as soon as the L1's consensus accepts the block. At this point the *unsigned* Warp message is on the L1's chain history.

The unsigned message is then aggregated into a signed message by an off-chain process. A **relayer** (or the user themselves) queries the L1's validator nodes — these are the same nodes that the P-chain listed in the `L1Validator` table — for partial BLS signatures. Each node, on request, fetches the unsigned message from its own copy of the L1's state, decides whether to sign it (typically: yes, if the message exists on its accepted chain), and returns a partial signature. The relayer aggregates the partials into a single BLS signature using `snow/validators/warp.go`'s canonicalization, packages the result with the original payload into a `warp.Message`, and submits it to the P-chain wrapped inside a `RegisterL1ValidatorTx`.

The P-chain's executor validates the signature against the L1's *current active validator set* at the P-chain block height the tx is included in. The set is queried via `validators.State.GetWarpValidatorSets(ctx, height)`, which canonicalizes only validators with `IsActive() == true` (`l1_validator.go:164-166`). Inactive validators contribute zero to the signing weight, so the L1 must always have enough active stake to meet the Warp quorum threshold, otherwise its messages are unverifiable on the P-chain. After signature verification, the executor's job is to apply the message to state. For `RegisterL1ValidatorTx` this means parsing the inner `RegisterL1Validator`, computing `validationID = hashing.ComputeHash256Array(msg.Bytes())` (`register_l1_validator.go:79-81`), building an `L1Validator` row, and writing it via `state.PutL1Validator` (see `standard_tx_executor.go:872-1027`).

Several layers of replay and freshness protection apply at this stage. They are listed below, but the most important is the `Expiry` field on `RegisterL1Validator`. Without an explicit expiry, an old unsigned-but-once-aggregable message could be cached forever and submitted years later. The executor enforces three windows on it (`standard_tx_executor.go:942-948`):

```text
if msg.Expiry <= currentTimestampUnix:                   reject (already expired)
if (msg.Expiry - currentTimestampUnix) > 24h:            reject (too far in the future)
if state.HasExpiry({Timestamp: msg.Expiry, ValidationID}): reject (replay)
```

The constant is `RegisterL1ValidatorTxExpiryWindow = day` (`standard_tx_executor.go:42`). On success the executor writes the `(Timestamp, ValidationID)` pair via `state.PutExpiry`, and a sweep eventually drops expired entries.

For weight changes (`SetL1ValidatorWeightTx`), replay protection is per-validator rather than per-message: each `L1Validator` row carries a `MinNonce`, and incoming `L1ValidatorWeight.Nonce` must be `>= MinNonce`. After a successful update the executor writes back `MinNonce = msg.Nonce + 1` (`standard_tx_executor.go:1163`). The nonce `math.MaxUint64` is reserved as a sentinel for removal — `SetL1ValidatorWeightTx` skips the increment for removals because the row is about to be deleted (`standard_tx_executor.go:1158-1163`). The message-level verifier rejects `(Nonce = MaxUint64, Weight != 0)` in `l1_validator_weight.go:31-36`.

#### 6.1.3 The P-chain → L1 confirmation model

The other half of the protocol is the manager contract's ability to read P-chain state. ACP-77 does not let the L1 contract poll the P-chain — there is no RPC pathway from a smart contract to off-chain JSON. Instead, the L1 obtains *signed assertions* about P-chain state, by asking P-chain validators to sign one of three message types:

- `L1ValidatorRegistration(validationID, registered)` — "this validator is/is not currently registered."
- `L1ValidatorWeight(validationID, nonce, weight)` — "this validator's current state is `(nonce, weight)`."
- `SubnetToL1Conversion(id)` — "this subnet's conversion ID is `id`."

The mechanism for producing these signatures is the **ACP-118 signature request protocol**. A relayer sends an `acp118.SignatureRequest` to a P-chain validator over the network, containing the unsigned message it wants signed plus an optional `justification` byte slice. The validator routes the request to its own `signatureRequestVerifier` (`vms/platformvm/network/warp.go:50-95`). The verifier inspects the message type, looks up the relevant P-chain state, and decides whether the claim being signed is true. If yes, the validator returns a partial BLS signature. The relayer aggregates partial signatures from a quorum of P-chain validators into a single BLS aggregate, packages it into a `warp.Message`, and delivers the message to the L1.

On the L1, the manager contract verifies the inbound `warp.Message` via the Warp precompile, which validates the aggregate signature against the P-chain's validator set as of a recent height. (The precompile, in turn, queries `validators.State.GetWarpValidatorSets` exposed via `snow/validators/warp.go`.) Once verified, the contract trusts the message body and updates its own bookkeeping.

The `signatureRequestVerifier`'s logic is what makes these signatures meaningful:

- **`L1ValidatorRegistration` with `Registered=true`** (`warp.go:165-188`): the verifier signs only if `state.GetL1Validator(validationID)` returns a row. If the validator does not exist on the P-chain, the request is rejected with `ErrValidationDoesNotExist`. This is what the manager queries after issuing a `RegisterL1Validator` to confirm the P-chain has accepted the registration.

- **`L1ValidatorRegistration` with `Registered=false`** (`warp.go:140-163`): the verifier requires a `justification` argument because non-registration is harder to attest to — the verifier must distinguish "this validator was never registered" from "this validator was registered and then removed." The justification is a protobuf describing either the original `ConvertSubnetToL1Tx` slot or the original `RegisterL1Validator` message; the verifier traces it forward to confirm the validation is no longer present and (for registration messages) that the expiry has passed (`warp.go:190-316`). The manager queries this when it wants to *prune* a registration request that the P-chain rejected for replay or expiry reasons.

- **`L1ValidatorWeight`** (`warp.go:318-359`): the verifier looks up the current `L1Validator`, and signs only if `(msg.Nonce + 1 == validator.MinNonce) && (msg.Weight == validator.Weight)`. This is what the manager queries after issuing a weight change to confirm the change has committed at the P-chain. Note the off-by-one — the L1 sends `Nonce = N` to mean "I want to sign a message saying this is my state after I issued the message with nonce N," and the P-chain only attests to it once `MinNonce` has been bumped past N.

- **`SubnetToL1Conversion`** (`warp.go:97-134`): the verifier requires the subnet ID as the `justification`, looks up the `SubnetToL1Conversion` record, and signs only if the message's `ID` matches the stored `ConversionID`. Used by the manager during initialization to verify it was deployed against a real, matching conversion.

Importantly, none of these P-chain → L1 messages mutate P-chain state. They are pure read-throughs: the P-chain's only role is to attest, with a BLS signature, to the truth of a claim about its own state at the height the validator's local view happens to be at. The L1's verifier later checks the signature against the P-chain validator set at *some* height — typically the latest known to the L1 via `GetCurrentValidatorSet` — so the signed claim implicitly carries a freshness window equal to the P-chain validator set's churn rate. This is by design.

#### 6.1.4 The full registration handshake

Putting it together, the lifecycle of a single L1 validator registration looks like this:

```
[1] User calls manager.initiateValidatorRegistration(...)
        │
        ▼  manager runs L1-specific business logic
[2] manager → Warp precompile sendWarpMessage(AddressedCall(RegisterL1Validator))
        │
        ▼  L1 block accepted; unsigned message committed to L1 history
[3] Relayer queries L1 validators, aggregates partial signatures
        │
        ▼  warp.Message with quorum signature now exists
[4] Relayer submits RegisterL1ValidatorTx{Message: ..., Balance: ...} to P-chain
        │
        ▼  P-chain executor:
        │    - verifies signature against L1 active set (GetWarpValidatorSets)
        │    - verifies sourceChainID/sourceAddress match SubnetToL1Conversion
        │    - verifies expiry, replay (PutExpiry)
        │    - verifies BLS proof-of-possession against the message's PublicKey
        │    - state.PutL1Validator(...)
        │
        ▼  P-chain block accepted; L1Validator row exists in state
[5] Relayer requests P-chain validators to sign
        L1ValidatorRegistration(validationID, Registered=true)
        │
        ▼  signatureRequestVerifier confirms registration
        │  Quorum of partial signatures aggregated into a warp.Message
        │
        ▼
[6] Relayer delivers the signed L1ValidatorRegistration back to the L1
[7] manager.completeValidatorRegistration(warpMessage)
        │
        ▼  manager runs Warp precompile verification, updates its own
        │  state to reflect "this validator is now active."
```

Without step [7], the manager does not know the registration succeeded, even though the P-chain has accepted it. Without step [4], the P-chain does not know the manager wanted the registration, even though the L1 has emitted the message. The two halves are independent communications, and the system tolerates either side being delayed or retried.

The lifecycle of a weight change is structurally identical, with `L1ValidatorWeight` taking the place of both message types (it is one of the two payloads where direction matters semantically).

#### 6.1.5 Anchoring identity: chain ID and source address

The manager's identity on the P-chain is fixed by the `(ChainID, Addr)` pair in `SubnetToL1Conversion`, written by `ConvertSubnetToL1Tx` (`standard_tx_executor.go:861`). Every subsequent L1 → P-chain message must originate from exactly that chain and that contract; the check happens in `verifyL1Conversion`:

```text
verifyL1Conversion(state, subnetID, sourceChainID, sourceAddress):
    conversion := state.GetSubnetToL1Conversion(subnetID)
    require sourceChainID == conversion.ChainID
    require sourceAddress == conversion.Addr
```

There is no upgrade path for the conversion record. If the L1 wants to migrate its manager to a new contract, it must do so *behind* the existing address — typically with a transparent proxy, EIP-1967-style, that keeps the storage contract at the original `Addr` and forwards calls to a new implementation contract. A misconfigured initial `Addr` permanently locks the L1 out of its own validator set, with no recourse short of redeploying the L1 from scratch.

The manager's identity on the L1 side is fixed by what the L1's `SubnetToL1ConversionData` says at conversion time. Specifically, `SubnetToL1ConversionData` (`subnet_to_l1_conversion.go:21-26`) commits to the manager's chain ID, address, and initial validators in a single hash. The manager contract's bootstrap logic typically queries P-chain validators for a `SubnetToL1Conversion` message with that hash, verifies it matches its own copy of the same data, and only then enables itself. This is why the conversion is permanent: the hash is what gives the manager confidence it is operating against the correct P-chain record, and changing the record after the fact would invalidate any manager that relied on the original.

#### 6.1.6 Where each piece lives in the codebase

| Responsibility | Location |
|---|---|
| ACP-77 message types and parsers | `vms/platformvm/warp/message/` |
| `AddressedCall` envelope | `vms/platformvm/warp/payload/` |
| Outer `warp.Message` and signature verification | `vms/platformvm/warp/` |
| Inbound (L1 → P-chain) handlers | `vms/platformvm/txs/executor/standard_tx_executor.go` (executors) |
| Outbound (P-chain → L1) signing decisions | `vms/platformvm/network/warp.go` (`signatureRequestVerifier`) |
| Validator-set-as-Warp-set canonicalization | `snow/validators/warp.go` |
| Reference manager contracts (Solidity) | `ava-labs/icm-contracts` (separate repository) |
| Warp precompile on `subnet-evm` | `ava-labs/subnet-evm` (separate repository) |

The only piece this document does not have a path to is the contract itself, which is intentional: ACP-77 deliberately puts the policy decisions outside avalanchego so that L1 operators can implement whichever staking, slashing, governance, or capacity rules they prefer, without ever needing to coordinate with the P-chain implementation.

#### 6.1.7 The cycle of authority

A common point of confusion is who signs what. The two cycles are:

- **L1 → P-chain messages** (`RegisterL1Validator`, `L1ValidatorWeight` as a command) are signed by the **L1's active validator set**, sampled from `L1Validator` rows with `IsActive() == true` at the P-chain height the receiving tx is included in. These are the validators the message is *about*.
- **P-chain → L1 messages** (`L1ValidatorRegistration`, `L1ValidatorWeight` as a confirmation, `SubnetToL1Conversion`) are signed by **P-chain validators**, sampled from the primary network validator set at the height the relayer is querying.

The L1's set vouching for changes to its own composition is the loop ACP-77 creates and is the reason `errRemovingLastValidator` exists: removing the last active validator would leave no signers capable of authorizing the *next* change, and the L1 would be permanently frozen from the P-chain's perspective. The guard ensures at least one signer always remains, and the continuous-fee mechanism does not violate it because depletion produces an *inactive* row (still in state, still recoverable by `IncreaseL1ValidatorBalanceTx`) rather than a removed one.

### 6.2 Mainnet specifics

Behavior is network-agnostic at the lifecycle level; mainnet differs only in numerical parameters and upgrade timing. The notable knobs:

- **`MinValidatorStake` and `MinDelegatorStake`** — checked in `vms/platformvm/txs/executor/staker_tx_verification.go`. Mainnet's values are larger than fuji's and far larger than local networks'.
- **Min and max staking durations** — same file. 14 days minimum and 365 days maximum on mainnet.
- **Reward parameters** — `vms/platformvm/reward/calculator.go`. Curve constants are mainnet-specific.
- **Etna activation timestamp** — configured in `upgrade/upgrade.go`. Tests that pretend Etna is always-on must be careful; transaction-level gating still respects the network's specific timestamp.
- **`ValidatorFeeConfig.Capacity` and `.Target`** — `vms/platformvm/validators/fee/fee.go`. `Capacity` is the global cap on simultaneously-active L1 validators and is the source of `errMaxNumActiveValidators` returned by `IncreaseL1ValidatorBalanceTx` when reactivating an inactive validator.

### 6.3 Common pitfalls

A non-exhaustive list of things that have caused real bugs:

1. **`Weight = 0` is a tombstone, not a value.** Setting it for any purpose other than removal will silently delete the L1 validator on the next diff flush.
2. **Inactive is not deleted.** A validator with `EndAccumulatedFee == 0` still occupies state. Tests that assume "the validator is inactive" implies "the row is gone" are wrong.
3. **Subnet conversion does not migrate legacy stakers.** They remain on disk after conversion, no longer authorize anything, and no longer surface in the validator-set exposure layer for the converted subnet.
4. **Last-validator guard only blocks `SetL1ValidatorWeightTx`.** Balance depletion alone cannot brick an L1; it can only render every validator inactive while keeping the rows on disk for future reactivation.
5. **`IncreaseL1ValidatorBalanceTx` is unauthenticated.** Anyone can fund anyone. Code that assumes only a designated owner tops up is wrong.
6. **Tx executors run after `AdvanceTimeTo`.** `e.state.GetTimestamp()` inside an executor returns the post-advancement timestamp, and the staker set has already been promoted/expired before the executor sees it.
7. **Diff direction is backwards.** `ApplyValidatorWeightDiffs` walks toward the past, so a forward-time `Decrease` becomes an addition to the running set during reconstruction.
8. **Staker `StartTime` is `time.Time`; L1Validator `StartTime` is `uint64`.** They serialize differently and are not interchangeable.
9. **The validator manager contract's address is fixed at conversion time.** A misconfigured manager means the L1 is permanently locked out from changing its own validator set.
10. **`putStaker` has three pre-Durango / post-Durango / post-Etna branches.** Tests written against one fork may break under a different upgrade configuration.

### 6.4 File map

For navigation:

| Topic | File |
|---|---|
| Tx structs, primary network legacy | `vms/platformvm/txs/add_validator_tx.go`, `add_delegator_tx.go` |
| Tx structs, permissionless | `vms/platformvm/txs/add_permissionless_validator_tx.go`, `add_permissionless_delegator_tx.go` |
| Tx structs, legacy subnet | `vms/platformvm/txs/add_subnet_validator_tx.go`, `remove_subnet_validator_tx.go`, `transfer_subnet_ownership_tx.go`, `create_subnet_tx.go`, `transform_subnet_tx.go` |
| Tx structs, L1 / Etna | `vms/platformvm/txs/convert_subnet_to_l1_tx.go`, `register_l1_validator_tx.go`, `set_l1_validator_weight_tx.go`, `increase_l1_validator_balance_tx.go`, `disable_l1_validator_tx.go` |
| Reward tx | `vms/platformvm/txs/reward_validator_tx.go` |
| Standard tx execution | `vms/platformvm/txs/executor/standard_tx_executor.go` |
| Proposal tx execution | `vms/platformvm/txs/executor/proposal_tx_executor.go` |
| Subnet authorization | `vms/platformvm/txs/executor/subnet_tx_verification.go` |
| Staker tx verification | `vms/platformvm/txs/executor/staker_tx_verification.go` |
| Time advancement | `vms/platformvm/txs/executor/state_changes.go` |
| Block verifier | `vms/platformvm/block/executor/verifier.go` |
| Block acceptor | `vms/platformvm/block/executor/acceptor.go` |
| Block builder | `vms/platformvm/block/builder/builder.go` |
| Staker state | `vms/platformvm/state/staker.go`, `vms/platformvm/state/stakers.go` |
| L1Validator state | `vms/platformvm/state/l1_validator.go` |
| State writer + diffs | `vms/platformvm/state/state.go`, `vms/platformvm/state/diff.go` |
| P-chain validators interface | `vms/platformvm/validators/manager.go` |
| Continuous-fee mechanism | `vms/platformvm/validators/fee/fee.go` |
| Cross-chain validator interface | `snow/validators/manager.go`, `snow/validators/state.go`, `snow/validators/warp.go` |
| Warp message types | `vms/platformvm/warp/message/` |
| Network upgrade gating | `upgrade/upgrade.go` |

### 6.5 Suggested reading order

For a contributor new to this code, the fastest path to a working mental model is:

1. Read `state/staker.go` and `state/l1_validator.go` end to end. The data model is small and every other file becomes more legible once you know it.
2. Read one Etna-era tx executor — `SetL1ValidatorWeightTx` is a good choice — tracing each call into `state.go`.
3. Read `state_changes.go`'s `AdvanceTimeTo` and `advanceValidatorFeeState`.
4. Read `block/executor/verifier.go`'s `BanffStandardBlock` and `deactivateLowBalanceL1Validators`.
5. Read `validators/manager.go`'s `GetValidatorSet` and `getCurrentValidatorSet`.
6. Skim ACP-77 ("Reinventing Subnets") for the design intent.

After that, the rest of the codebase reads as elaborations on these five points.
