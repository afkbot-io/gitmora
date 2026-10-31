# Gitmora accounts

The web application, hosted password forms under `/auth/`, and CLI consent use one account. Project access remains local to Gitmora. A new account has no project membership or operator role; an installation operator assigns membership separately.

## Registration policy

Public registration is enabled on the selected `gitmora.tasktopia.online` installation at the user's request. Use “Создать аккаунт” on the public page. This creates an ordinary account; project membership is assigned separately. New installations still default to closed registration.

Set `WORKGRAPH_REGISTRATION_ENABLED=false` in the private application `runtime.env` to close registration. The default is `false`; only literal `true` opens it. Invalid values stop startup. Restart the selected API so it applies the policy, then apply the same value to the hosted provider:

```sh
WORKGRAPH_REGISTRATION_ENABLED=false gitmorad identity-server configure --directory /srv/gitmora/identity
```

`identity-server up` also applies the env policy after an active provider starts. Keep the API and provider values consistent. With signup closed, `/v1/auth/register` denies requests, stale API instances cannot provision accounts against the durable disabled policy, and the provider rejects previously opened registration forms after its policy is applied. Existing login/browser/CLI authority is preserved; authentication stays mandatory.

Changing the provider issuer/client/session security policy remains a separate operation that deliberately revokes incompatible grants. Disabling registration is not such an operation.

## Create a user on a selected Docker installation

Run the host operator binary with a terminal; the private provider directory belongs to the operator, and is never mounted into the application API:

```sh
gitmorad users create \
  --directory /srv/gitmora/identity \
  --installation /srv/gitmora/state \
  --username engineer \
  --name 'Имя Фамилия'
```

The command prompts twice without echo. Passwords must contain at least twelve characters and are never passed in argv/env or printed. The host contacts only its selected provider's loopback administrative gateway; binding runs through the selected existing API container using its normal database secret. The command works with public registration disabled.

For a development deployment with a directly reachable database, omit `--installation` and configure `WORKGRAPH_DATABASE_URL` or `WORKGRAPH_DATABASE_URL_FILE`. Do not print or paste that secret into chat.

Keycloak assigns the actual subject. A confirmed provider creation followed by failed local storage reports the subject and can be resumed with the same command plus `--subject <provider UUID>`. Recovery verifies the exact enabled subject and username; it never changes an existing password. An uncertain provider creation must be inspected before retrying; existing usernames are not silently taken over.

The database-owner `users bind-subject` subcommand is the internal Docker/recovery boundary and accepts only the API's exact configured issuer. It is not a public API. Disabling an account or binding remains an application operator action and does not resurrect through registration.

The provider may request completion of its contact profile on first login. Email/name claims are display/profile data and never bind an existing Gitmora account or grant project authority. Browser login uses PKCE/nonce, one-use proof and protected cookies; CLI credentials remain in the OS secret store.
