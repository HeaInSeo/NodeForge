# TrueNAS NFS Runbook

HP ProLiant MicroServer Gen8 기반 TrueNAS 테스트 스토리지 연결 메모.
이 문서는 현재 실환경에서 확인한 값과, 다른 분석 서버에 같은 방식으로 붙이는 절차를 함께 정리한다.

## 현재 구성

- TrueNAS IP: `172.30.1.111`
- iLO IP: `172.30.1.17`
- TrueNAS pool: `RaidZ`
- NFS dataset: `NFSset`
- NFS export path: `/mnt/RaidZ/NFSset`
- 이 서버의 마운트 경로: `/mnt/genomics-test`
- 이 서버의 Tailscale IP: `100.92.45.46`

## 왜 이렇게 구성했는가

- 기존 TrueNAS 주소가 `192.168.0.111`이라 분석 서버 대역(`172.30.1.0/24`)과 달랐다.
- 서버마다 임시 보조 IP를 붙이는 방식은 운영이 번거로워서 TrueNAS를 `172.30.1.111/24`로 옮겼다.
- 외부 접속은 Tailscale을 쓰더라도, 서버와 NAS 사이 데이터 경로는 같은 LAN에서 NFS로 두는 편이 단순하다.

## TrueNAS 설정 상태

### 네트워크

- 관리 UI 주소를 `172.30.1.111/24`로 변경
- 기본 게이트웨이: `172.30.1.254`
- DNS는 `Network -> Global Configuration`에서 설정

### NTP

- 타임존: `Asia/Seoul`
- NTP 서버:
  - `kr.pool.ntp.org`
  - `pool.ntp.org`

### NFS

- 공유 대상 dataset: `/mnt/RaidZ/NFSset`
- Authorized Networks: `172.30.1.0/24`
- NFS 서비스: 활성화 및 자동 시작

## 이 서버에서 현재 사용 중인 마운트 설정

### 수동 마운트

```bash
sudo mkdir -p /mnt/genomics-test
sudo mount -t nfs 172.30.1.111:/mnt/RaidZ/NFSset /mnt/genomics-test
```

### 자동 마운트

`/etc/fstab`:

```fstab
172.30.1.111:/mnt/RaidZ/NFSset /mnt/genomics-test nfs defaults,_netdev 0 0
```

적용 및 확인:

```bash
sudo systemctl daemon-reload
sudo mount -a
df -h /mnt/genomics-test
```

## 다른 분석 서버에 같은 방식으로 붙이기

### 1. 네트워크 확인

- 해당 서버가 `172.30.1.0/24` 대역에 직접 닿아야 한다.
- 먼저 `ping 172.30.1.111` 이 되는지 확인한다.

### 2. 마운트 지점 생성

```bash
sudo mkdir -p /mnt/genomics-test
```

### 3. 수동 마운트

```bash
sudo mount -t nfs 172.30.1.111:/mnt/RaidZ/NFSset /mnt/genomics-test
```

### 4. 동작 확인

```bash
df -h /mnt/genomics-test
ls -la /mnt/genomics-test
touch /mnt/genomics-test/.mount-check
```

### 5. 자동 마운트 등록

`/etc/fstab` 맨 아래에 추가:

```fstab
172.30.1.111:/mnt/RaidZ/NFSset /mnt/genomics-test nfs defaults,_netdev 0 0
```

적용:

```bash
sudo systemctl daemon-reload
sudo mount -a
```

### 6. 필요 패키지

배포판에 따라 NFS 클라이언트 패키지가 필요할 수 있다.

- RHEL/Rocky/Alma/Fedora 계열: `nfs-utils`
- Debian/Ubuntu 계열: `nfs-common`

## 유전체 분석용 권장 디렉터리 구조

NAS에는 입력 데이터와 결과 보관 위주로 두고, 중간 작업파일은 로컬 디스크를 쓴다.

예시:

```text
/mnt/genomics-test/
  references/
  fastq/
  bam/
  results/
  shared-notes/
```

로컬 디스크 권장 항목:

- aligner temp
- sort temp
- large intermediate BAM/CRAM scratch
- workflow engine workdir

권장 패턴:

- 입력 데이터: `/mnt/genomics-test/fastq`
- reference: `/mnt/genomics-test/references`
- 최종 결과: `/mnt/genomics-test/results`
- 중간 산출물: 서버 로컬 SSD

## iLO 운영 메모

- iLO IP: `172.30.1.17`
- 구형 iLO라 HTTPS 호환 이슈가 있을 수 있다.
- 원격에서 브라우저로 열 때는 로컬 PC에서 SSH 터널을 여는 방식이 안정적이다.

예시:

```bash
ssh -fN -L 8443:172.30.1.17:443 heain@100.92.45.46
```

그 다음 로컬 브라우저:

```text
https://localhost:8443
```

## 기억할 점

- TrueNAS와 iLO 비밀번호는 서로 다르다.
- TrueNAS가 다른 대역에 있으면 NFS 마운트보다 네트워크 우회 설정이 먼저 꼬인다.
- 파일 시간이 이상하면 NTP와 타임존부터 본다.
- NFS는 여러 서버가 읽는 용도에 좋지만, 고I/O 중간파일 작업은 로컬 디스크가 낫다.
- 테스트용으로는 결과물만 NAS에 남기고 중간파일은 지우는 습관이 관리에 유리하다.

## 빠른 점검 명령

TrueNAS 핑:

```bash
ping 172.30.1.111
```

마운트 상태:

```bash
df -h /mnt/genomics-test
mount | grep genomics-test
```

쓰기 테스트:

```bash
touch /mnt/genomics-test/.write-check
ls -l /mnt/genomics-test/.write-check
```
