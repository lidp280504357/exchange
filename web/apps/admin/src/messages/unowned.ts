// The strings of the deposits of nobody (B7a, C5.5 ㉑): a custodian's
// deposit to an address no user has, credited to the user an
// administrator names; merged into the console's messages in i18n.ts.
export const unownedZh = {
  admin: {
    enum: {
      approvalKind: { DEPOSIT_ASSIGN: "无主充值记给用户" },
      depositReason: { UNKNOWN_ADDRESS: "地址无人使用（无主）" },
    },
    funds: {
      escalation: { NOT_ADDRESS_HOLDER: "记给的用户不是地址的持有人（含退役前的），不论金额都需另一位管理员决定" },
      escalationShort: { NOT_ADDRESS_HOLDER: "非地址持有人" },
    },
    unowned: {
      nobody: "无主",
      nobodyHint: "托管方报到账、但这个地址现在不属于任何用户（探测地址、已退役的地址）。资金记在「待处理充值」，查明归属后记给用户，或驳回。",
      owner: "地址的持有人",
      ownerRetired: "地址退役前的持有人",
      ownerNone: "没有记录",
      ownerHint: "仅供参考：地址的持有人不一定就是付款人，请先核实转账。",
      assign: "记给用户",
      assignTitle: "把这笔无主充值记给用户",
      assignHint:
        "按原币种、原数量从「待处理充值」转入所选用户的现货账户，钱包与账本各记一条审计。单人模式下不超过单笔限额时立即执行，否则等另一位管理员批准。确认词为用户 ID 的后 4 位。",
      userId: "用户 ID",
      userHint: "要记给的用户（UUID）",
      badUser: "请输入用户 ID（UUID）",
      useOwner: "填入地址的持有人",
      notHolder: "所选用户不是地址的持有人：不论金额，都要另一位管理员在「审批」里决定。",
      deposit: "无主充值 {{id}}",
    },
  },
  errors: {
    ADMIN_DEPOSIT_NOT_UNOWNED: "只有等待处理的无主充值才能记给用户",
    ADMIN_DEPOSIT_ASSIGN_OPEN: "这笔充值已有一个「记给用户」的申请在等待或已执行：先在「审批」里决定或撤回它",
    WALLET_DEPOSIT_NO_OWNER: "无主充值不能直接入账，请用「记给用户」",
  },
};

export const unownedEn = {
  admin: {
    enum: {
      approvalKind: { DEPOSIT_ASSIGN: "Deposit of nobody to a user" },
      depositReason: { UNKNOWN_ADDRESS: "Address of nobody" },
    },
    funds: {
      escalation: {
        NOT_ADDRESS_HOLDER: "Credited to a user other than the address's holder (now or before it was retired): a second administrator decides, whatever its worth",
      },
      escalationShort: { NOT_ADDRESS_HOLDER: "Not the holder" },
    },
    unowned: {
      nobody: "Nobody",
      nobodyHint:
        "The custodian reported it, but the address belongs to no user now (a probe, a retired address). The funds are in Unclaimed deposits: credit them to the user they turn out to be, or reject them.",
      owner: "The address's holder",
      ownerRetired: "The address's holder before it was retired",
      ownerNone: "No record",
      ownerHint: "A hint only: the address's holder is not always the sender; check the transfer first.",
      assign: "Credit to a user",
      assignTitle: "Credit this deposit of nobody to a user",
      assignHint:
        "Its own asset and amount move from Unclaimed deposits to the chosen user's spot account, audited by the wallet and the ledger. In single-person mode within the single-operation limit it is done at once, else a second administrator approves it. The confirmation word is the user ID's last four characters.",
      userId: "User ID",
      userHint: "The user it is credited to (a UUID)",
      badUser: "Enter a user ID (UUID)",
      useOwner: "Use the address's holder",
      notHolder: "The user is not the address's holder: a second administrator decides in Approvals, whatever its worth.",
      deposit: "Deposit of nobody {{id}}",
    },
  },
  errors: {
    ADMIN_DEPOSIT_NOT_UNOWNED: "Only a deposit of nobody waiting for a decision is credited to a user",
    ADMIN_DEPOSIT_ASSIGN_OPEN: "A request to credit this deposit waits or was carried out: decide or withdraw it in Approvals first",
    WALLET_DEPOSIT_NO_OWNER: "A deposit of nobody is not credited as it is: use Credit to a user",
  },
};
