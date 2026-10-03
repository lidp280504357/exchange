// The strings of fund operations whose attempt did not finish and of the
// Idempotency-Keys the dialogs send (C5.5 ⑥); merged into the console's
// messages in i18n.ts, over the older wording of the same keys.
export const attemptsZh = {
  admin: {
    attempts: {
      badge: "待核对",
      hint: "上次执行没有完成，可能已经记账：只能「完成」（不会重复记账），不能拒绝或撤回。",
      finishHelp: "上次执行没有完成，可能已经记账。完成会用同一个幂等键再执行一次：已记账的返回原来的分录，没记账的现在记账。",
      conflict: "这次提交和这个对话框里上一次结果未知的提交不一样。请先到「审批」核对那一笔；要发起新的操作，关掉对话框再打开。",
    },
    funds: {
      unknownHint: "操作（…{{id}}）保持待处理，标为「待核对」。可在这里再次确认，或到「审批」点「完成」，都不会重复记账。",
      finishTitle: "完成这笔操作：再执行一次，已记账的不会重复记账",
    },
    withdrawals: {
      alone: "单人模式：申请时和按当前价都不超过 {{max}} USDT 的提现，你的批准即可完成",
    },
  },
  errors: {
    ADMIN_APPROVAL_ATTEMPTED: "这笔操作上次执行没有完成、可能已经记账：只能完成，不能拒绝或撤回",
  },
};

export const attemptsEn = {
  admin: {
    attempts: {
      badge: "To check",
      hint: "Its last attempt did not finish and may have booked it: it can only be finished (never booked twice), not rejected or withdrawn.",
      finishHelp:
        "Its last attempt did not finish and may have booked it. Finishing carries it out again under the same idempotency key: booked already, the journal comes back; not booked, it is booked now.",
      conflict:
        "This submission differs from this dialog's last one, whose outcome is unknown. Check that one under Approvals first; to start another operation, close the dialog and open it again.",
    },
    funds: {
      unknownHint:
        "The operation (…{{id}}) stays pending, marked To check. Confirm again here or Finish it under Approvals: it is never booked twice.",
      finishTitle: "Finish this operation: carried out again, it is never booked twice",
    },
    withdrawals: {
      alone: "Single-person mode: your approval completes a withdrawal worth up to {{max}} USDT when requested and at the current price",
    },
  },
  errors: {
    ADMIN_APPROVAL_ATTEMPTED: "Its last attempt did not finish and may have booked it: finish it; it cannot be rejected or withdrawn",
  },
};
