// Global JS context objects injected by server-rendered templates.

declare const TRAINER_CTX: {
  username: string;
  isMod: boolean;
  loggedIn: boolean;
  isOwnProfile: boolean;
  csrfToken: string;
};
