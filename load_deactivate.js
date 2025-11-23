import http from "k6/http"
import { check, sleep } from "k6"
import { randomItem } from "https://jslib.k6.io/k6-utils/1.4.0/index.js"

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080"

export const options = {
  scenarios: {
    deactivate: {
      executor: "constant-arrival-rate",
      rate: 5,
      timeUnit: "1s",
      duration: __ENV.DURATION || "30s",
      preAllocatedVUs: 5,
      maxVUs: 20,
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<300"],
    http_req_failed: ["rate<0.001"],
    "http_req_duration{endpoint:team_deactivate}": ["p(50)<100", "p(95)<300"],
  },
}

const jsonHeaders = {
  headers: {
    "Content-Type": "application/json",
  },
}

function uid() {
  return (
    Math.random().toString(36).slice(2) +
    Math.random().toString(36).slice(2)
  )
}

export function setup() {
  const teamsCount = Number(__ENV.TEAMS || 20)
  const usersPerTeam = Number(__ENV.USERS_PER_TEAM || 10)
  const prsPerTeam = Number(__ENV.PRS_PER_TEAM || 10)

  const teamNames = []

  for (let t = 1; t <= teamsCount; t++) {
    const teamName = `load-team-${uid()}`
    const members = []
    const userIds = []

    for (let u = 1; u <= usersPerTeam; u++) {
      const userId = `user_${uid()}`
      members.push({
        user_id: userId,
        username: `User ${t}-${u}`,
        is_active: true,
      })
      userIds.push(userId)
    }

    const teamRes = http.post(
      `${BASE_URL}/team/add`,
      JSON.stringify({
        team_name: teamName,
        members,
      }),
      jsonHeaders,
    )

    check(teamRes, {
      "team/add status is 201": (r) => r.status === 201,
    })

    if (teamRes.status !== 201) {
      throw new Error(`setup failed for team ${teamName}: ${teamRes.status} ${teamRes.body}`)
    }

    for (let p = 1; p <= prsPerTeam; p++) {
      const prId = `pr_${uid()}`
      const authorId = randomItem(userIds)
      const prRes = http.post(
        `${BASE_URL}/pullRequest/create`,
        JSON.stringify({
          pull_request_id: prId,
          pull_request_name: `Load PR ${prId}`,
          author_id: authorId,
        }),
        jsonHeaders,
      )

      check(prRes, {
        "create PR status is 201": (r) => r.status === 201,
      })

      if (prRes.status !== 201) {
        throw new Error(`setup failed for PR ${prId}: ${prRes.status} ${prRes.body}`)
      }
    }

    teamNames.push(teamName)
  }

  return { teamNames }
}

export default function (data) {
  const teamName = randomItem(data.teamNames)

  const res = http.post(
    `${BASE_URL}/team/deactivateMembers`,
    JSON.stringify({ team_name: teamName }),
    {
      ...jsonHeaders,
      tags: { endpoint: "team_deactivate" },
    },
  )

  check(res, {
    "deactivateMembers status is 200": (r) => r.status === 200,
  })

  const pause = Number(__ENV.PAUSE || 0.1)
  if (pause > 0) {
    sleep(pause)
  }
}
