<div align="center">

# 实验指导：Git & Gitee 篇

<sub>适用于课程实验仓库克隆、组队协作、远程仓库配置与提交推送</sub>

</div>

---

> [!info] 文档说明
> 本文档用于指导你完成课程实验仓库的获取、Gitee 私有仓库创建、组员协作配置，以及本地代码提交与远程推送。

> [!tip] 使用建议
> 建议按本文顺序逐步操作，不要跳步骤。尤其是 `SSH 公钥配置`、`组员邀请`、`仓库地址提交` 这三部分，最容易遗漏。

## 目录

- [1. 下载 Git](#1-下载-git)
- [2. 克隆课程实验仓库到本地](#2-克隆课程实验仓库到本地)
- [3. 跟踪最新实验材料](#3-跟踪最新实验材料)
- [4. 在 Gitee 上创建自己的仓库](#4-在-gitee-上创建自己的仓库)
- [5. 将本地仓库关联 Gitee 远程仓库](#5-将本地仓库关联-gitee-远程仓库)
- [6. 提交修改示例](#6-提交修改示例)
- [7. 参考资料](#7-参考资料)

---

## 1. 下载 Git

### 目标

安装分布式版本控制系统 `Git`。

### 命令

```bash
sudo apt-get install git
```

> [!note] 说明
> 如果你的系统已经安装 Git，可以跳过这一步，并用 `git --version` 检查版本。

---

## 2. 克隆课程实验仓库到本地

### 目标

将课程实验仓库下载到本地，作为后续实验的基础目录。

### 命令

```bash
git clone https://gitee.com/hnu-cloudcomputing/cloud-compute-book-code.git
```

> [!tip] 结果
> 执行完成后，当前目录下会出现一个名为 `cloud-compute-book-code` 的仓库目录。

---

## 3. 跟踪最新实验材料

### 目标

进入本地仓库，并同步课程仓库中的最新内容。

### 命令

```bash
# 进入仓库目录
cd cloud-compute-book-code

# 获取最新课程实验仓库的内容
git pull
```

> [!important] 注意
> 每次开始实验前，建议先执行一次 `git pull`，避免本地代码落后于课程仓库。

---

## 4. 在 Gitee 上创建自己的仓库

### 目标

在 Gitee 上创建一份属于你们小组的私有仓库，用于后续实验提交与协作。

### 步骤 1：新建仓库

点击右上角 `+` 号，选择“新建仓库”。

![[Pasted image 20260316215850.png|697]]

### 步骤 2：填写仓库信息

填写仓库名称，并勾选“私有”选项。

![[Pasted image 20260316223306.png]]

### 步骤 3：进入仓库管理

创建完成后，在仓库界面点击“管理”。

![[Pasted image 20260316215950.png|697]]

### 步骤 4：添加组员

点击“仓库成员管理”，然后选择“邀请用户”，将自己组的组员添加为“管理员”或者“开发者”。

![[Pasted image 20260316220021.png]]

### 步骤 5：发送邀请链接

复制链接给组员，组员点击后同意邀请。

![[Pasted image 20260316220123.png]]

### 步骤 6：填写仓库地址和邀请链接

创建好仓库后，在指定链接中填写： https://docs.qq.com/form/page/DRXZmWUxaT2t2YnJx

- 仓库地址
- 邀请链接

> [!warning] 重点提醒
> 邀请用户时不要勾选“需要管理员审核”，并且邀请链接有效期只有三天，生成后请及时填写提交。

### 仓库地址获取方式

进入你们自己的仓库，点击“克隆/下载”。

![[Pasted image 20260316221222.png|697]]

使用 `HTTPS` 格式的仓库地址。

![[Pasted image 20260316221322.png]]

---

## 5. 将本地仓库关联 Gitee 远程仓库

### 5.1 使用 HTTPS 关联远程仓库

```bash
# 使用你们自己仓库的 HTTPS 地址
git remote add origin HTTPS地址
```

> [!note] 示例
> `HTTPS地址` 需要替换成你们自己 Gitee 仓库页面里的真实地址，不要直接复制本文中的占位文字。

### 5.2 推荐使用 SSH 方式

#### 第一步：检查是否已有 SSH 密钥

```bash
# 如果看到类似 id_rsa.pub 的文件，说明已经存在 SSH 密钥
ls -al ~/.ssh
```

#### 第二步：如果没有，则生成 SSH 密钥

```bash
ssh-keygen -t rsa -b 4096 -C "你的邮箱"
```

#### 第三步：查看公钥并复制

```bash
cat ~/.ssh/id_rsa.pub
```

#### 第四步：在 Gitee 中添加个人公钥

然后在个人设置页面点击“SSH公钥”，填写标题并把公钥复制进去。

![[Pasted image 20260316222342.png|404]]

![[Pasted image 20260316222450.png]]

> [!important] 说明
> “个人公钥”配置完成后，才能够将自己的修改 `push` 到远程仓库。

#### 第五步：把远程仓库地址改成 SSH

SSH 地址为点击“克隆/下载”后的第二个标签，例如：

![[Pasted image 20260316221922.png]]

```bash
# 使用你们自己的仓库 SSH 地址
git remote set-url origin SSH地址
```

> [!tip] 推荐
> 如果你后续会频繁提交代码，建议优先使用 `SSH`，比 `HTTPS` 更方便。

---

## 6. 提交修改示例

### 第一步：完成 Git 全局配置

```bash
# your_name 和 your_email 换成自己的信息
git config --global user.name "your_name"
git config --global user.email "your_email"
```

### 第二步：把单个文件加入暂存区

例如在本地仓库中创建了一个 `README.md` 文档，把更改推送到暂存区：

```bash
git add README.md
```

### 第三步：把所有修改加入暂存区

如果需要把所有修改都加入暂存区：

```bash
git add .
```

### 第四步：提交修改

把暂存区中的修改提交到当前分支：

```bash
git commit -m "first commit"
```

### 第五步：推送到远程仓库

把本地仓库内容推送到远程仓库：

```bash
git push -u origin master
```

> [!note] 说明
> 如果要推送到其他分支，则将 `master` 修改为对应分支名。

---

## 7. 参考资料

- [简介 - Git教程 - 廖雪峰的官方网站](https://liaoxuefeng.com/books/git/introduction/index.html)
- [Gitee 帮助中心 - Gitee.com](https://gitee.com/help/)

> [!tip] 建议
> 建议详细阅读以上参考资料，熟悉并建立完整的协作流程。

---

## 操作检查清单

- [ ] 已安装 Git
- [ ] 已克隆课程实验仓库
- [ ] 已执行 `git pull` 同步最新材料
- [ ] 已在 Gitee 创建小组私有仓库
- [ ] 已邀请组员加入仓库
- [ ] 已填写仓库地址和邀请链接
- [ ] 已配置 HTTPS 或 SSH 远程仓库
- [ ] 已完成一次 `add / commit / push`

---

<div align="center">
<sub>完成以上步骤后，你们的小组仓库就可以正式用于课程实验协作。</sub>
</div>
