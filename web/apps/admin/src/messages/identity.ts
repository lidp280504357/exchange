// The strings of the users' usernames and avatars in the console (design
// 2026-10-07, avatars and usernames §1.6, batch I3), merged by
// pageMessages.ts.
export const identityZh = {
  admin: {
    users: { username: "用户名" },
    user: {
      username: "用户名",
      avatar: "头像",
      defaultAvatar: "默认头像",
      resetUsername: "重置用户名",
      resetUsernameTitle: "重置 {{name}} 的用户名",
      resetUsernameHint: "改为随机的 user_ 加 8 位字母数字，用户收到站内信，可以马上另选一个（不进 7 天冷却）。用于处理违规用户名。",
      usernameReset: "用户名已重置为 {{name}}",
      resetAvatar: "重置头像",
      resetAvatarTitle: "重置 {{name}} 的头像",
      resetAvatarHint: "删除用户上传的头像、换回默认头像，用户收到站内信，可以重新上传。用于处理违规头像。",
      noAvatar: "用户用的是默认头像，没有可重置的",
      avatarReset: "头像已换回默认",
    },
  },
};

export const identityEn = {
  admin: {
    users: { username: "Username" },
    user: {
      username: "Username",
      avatar: "Avatar",
      defaultAvatar: "Default avatar",
      resetUsername: "Reset the username",
      resetUsernameTitle: "Reset {{name}}'s username",
      resetUsernameHint: "It becomes a random user_ and 8 letters or digits; the user is told in the app and may pick another at once (no 7-day wait). For a username that breaks the rules.",
      usernameReset: "The username is now {{name}}",
      resetAvatar: "Reset the avatar",
      resetAvatarTitle: "Reset {{name}}'s avatar",
      resetAvatarHint: "The uploaded avatar is deleted and the default one shown; the user is told in the app and may upload another. For an avatar that breaks the rules.",
      noAvatar: "The user has the default avatar: nothing to reset",
      avatarReset: "Back to the default avatar",
    },
  },
};
